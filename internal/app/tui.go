package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

func RunTUI(api string) error {
	if runtime.GOOS != "windows" {
		_ = exec.Command("stty", "-echo", "-icanon", "min", "1", "time", "0").Run()
		defer exec.Command("stty", "sane").Run()
	}
	fmt.Print("\x1b[?1049h\x1b[?25l")
	defer fmt.Print("\x1b[?25h\x1b[?1049l")
	keys := make(chan byte, 8)
	go func() {
		b := make([]byte, 1)
		for {
			if _, err := os.Stdin.Read(b); err != nil {
				return
			}
			select {
			case keys <- b[0]:
			default:
			}
		}
	}()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	notice := ""
	for {
		st, err := fetchStatsSync(api)
		renderDashboard(st, err, notice)
		select {
		case k := <-keys:
			switch strings.ToLower(string(k)) {
			case "q":
				return nil
			case "h":
				notice = postActionSync(api, "harvest")
			case "e", "p":
				notice = postActionSync(api, "export")
			}
		case <-t.C:
		}
	}
}
func fetchStatsSync(api string) (Stats, error) {
	c := http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get("http://" + api + "/api/stats")
	if err != nil {
		return Stats{}, err
	}
	defer resp.Body.Close()
	var st Stats
	err = json.NewDecoder(resp.Body).Decode(&st)
	return st, err
}
func postActionSync(api, action string) string {
	c := http.Client{Timeout: 2 * time.Second}
	req, _ := http.NewRequest(http.MethodPost, "http://"+api+"/api/action/"+action, nil)
	resp, err := c.Do(req)
	if err != nil {
		return "action failed: " + err.Error()
	}
	resp.Body.Close()
	return action + " queued"
}
func renderDashboard(st Stats, err error, notice string) {
	fmt.Print("\x1b[H\x1b[2J")
	status := "● RUNNING"
	if err != nil {
		status = "○ API OFFLINE"
	}
	fmt.Printf("SYND·PROXY                                             %s\n", status)
	fmt.Println(strings.Repeat("─", 72))
	fmt.Printf("DISCOVERED  %9d   ALIVE NOW   %9d   GOLD NOW   %9d\n", st.Total, st.Alive, st.Gold)
	fmt.Printf("EVER ALIVE  %9d   EVER GOLD   %9d   DEAD NOW   %9d\n", st.EverAlive, st.EverGold, st.DeadNow)
	fmt.Printf("CHECKED     %9d   QUEUED      %9d   IN-FLIGHT  %9d\n", st.Checked, st.Queue, st.InFlight)
	fmt.Printf("NEW/HARVEST %8d   QUEUED/HRV  %9d   WORKERS    %9d\n", st.NewThisHarvest, st.QueuedThisHarvest, st.Workers)
	fmt.Printf("SOURCE FEEDS %7d   FETCHED/HRV %9d   DUE %4d   ERR %4d\n", st.SourceFeeds, st.SourcesFetchedThisHarvest, st.SourcesDue, st.SourceErrors)
	fmt.Println()
	fmt.Printf("HTTP        %9d   HTTPS       %9d\n", st.AliveProtocols["http"], st.AliveProtocols["https"])
	fmt.Printf("SOCKS4      %9d   SOCKS5      %9d\n", st.AliveProtocols["socks4"], st.AliveProtocols["socks5"])
	fmt.Println()
	fmt.Printf("SESSION OK  %9d   SESSION FAIL %8d\n", st.Successes, st.Failures)
	fmt.Printf("HARVESTS    %9d   CYCLES      %9d   GIT %s\n", st.Harvests, st.Cycles, st.GitStatus)
	fmt.Printf("PHASE       %-20s\n", st.CyclePhase)
	fmt.Printf("LAST HARVEST  %s\nLAST EXPORT   %s\nLAST PUSH     %s\nLAST ARCHIVE  %s\nLAST CYCLE    %s\n", fmtTime(st.LastHarvest), fmtTime(st.LastExport), fmtTime(st.LastGitPush), fmtTime(st.LastArchive), fmtTime(st.LastCycleEnd))
	fmt.Println(strings.Repeat("─", 72))
	fmt.Println("[H] Force all sources   [E/P] Export + Push   [Q] Quit")
	if notice != "" {
		fmt.Println("\n" + notice)
	}
	if err != nil {
		fmt.Println("\nAPI error: " + err.Error())
	}
	fmt.Println("\nSOURCES")
	max := 14
	if len(st.SourceStatus) < max {
		max = len(st.SourceStatus)
	}
	for i := 0; i < max; i++ {
		s := st.SourceStatus[i]
		mark := "ok"
		if s.Error != "" {
			mark = "ERR"
		}
		next := "due"
		if !s.NextFetch.IsZero() && s.NextFetch.After(time.Now()) {
			d := time.Until(s.NextFetch).Round(time.Second)
			next = d.String()
		}
		fmt.Printf("%-26s %4s  found=%-7d %5dms next=%s\n", s.Name, mark, s.Unique, s.DurationMS, next)
	}
}
func fmtTime(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}
