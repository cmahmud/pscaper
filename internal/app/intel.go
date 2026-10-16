package app

import (
	"bufio"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ASNInfo struct {
	ASN         uint32 `json:"asn"`
	ASCountry   string `json:"as_country"`
	Description string `json:"as_description"`
}
type asnRange struct {
	Start, End, ASN      uint32
	Country, Description string
}
type ASNDatabase struct {
	mu       sync.RWMutex
	path     string
	ranges   []asnRange
	modified time.Time
}

func NewASNDatabase(path string) *ASNDatabase { return &ASNDatabase{path: path} }
func (db *ASNDatabase) Reload() error {
	stat, err := os.Stat(db.path)
	if err != nil {
		return err
	}
	db.mu.RLock()
	if !db.modified.IsZero() && stat.ModTime().Equal(db.modified) {
		db.mu.RUnlock()
		return nil
	}
	db.mu.RUnlock()
	f, err := os.Open(db.path)
	if err != nil {
		return err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer zr.Close()
	scanner := bufio.NewScanner(zr)
	scanner.Buffer(make([]byte, 8192), 1024*1024)
	ranges := make([]asnRange, 0, 600000)
	for scanner.Scan() {
		fields := strings.SplitN(scanner.Text(), "\t", 5)
		if len(fields) != 5 {
			continue
		}
		first, a := strconv.ParseUint(fields[0], 10, 32)
		last, b := strconv.ParseUint(fields[1], 10, 32)
		asn, c := strconv.ParseUint(fields[2], 10, 32)
		if a != nil || b != nil || c != nil || first > last {
			continue
		}
		country := fields[3]
		if country == "None" {
			country = ""
		}
		desc := fields[4]
		if len(desc) > 180 {
			desc = desc[:180]
		}
		ranges = append(ranges, asnRange{Start: uint32(first), End: uint32(last), ASN: uint32(asn), Country: country, Description: desc})
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if len(ranges) == 0 {
		return fmt.Errorf("empty ASN dataset")
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].Start < ranges[j].Start })
	db.mu.Lock()
	db.ranges = ranges
	db.modified = stat.ModTime()
	db.mu.Unlock()
	return nil
}
func (db *ASNDatabase) Lookup(host string) (ASNInfo, bool) {
	addr, err := netip.ParseAddr(host)
	if err != nil || !addr.Is4() {
		return ASNInfo{}, false
	}
	b := addr.As4()
	n := binary.BigEndian.Uint32(b[:])
	db.mu.RLock()
	defer db.mu.RUnlock()
	k := sort.Search(len(db.ranges), func(i int) bool { return db.ranges[i].Start > n }) - 1
	if k < 0 || n > db.ranges[k].End {
		return ASNInfo{}, false
	}
	rec := db.ranges[k]
	if rec.ASN == 0 {
		return ASNInfo{}, false
	}
	return ASNInfo{ASN: rec.ASN, ASCountry: rec.Country, Description: rec.Description}, true
}
