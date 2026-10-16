#!/usr/bin/env python3
"""Bounded, read-only pscaper observation on the current VPS.

Samples once a minute for an hour. Stores only process/resource and existing
public scraper metrics, never proxy addresses, secrets, or external traffic.
This can run after a deployment to measure peak RAM, CPU and pool trends.
"""
import argparse
import datetime as dt
import json
import os
import pathlib
import subprocess
import time
import urllib.request

ROOT=pathlib.Path(__file__).resolve().parents[1]
def read_usage(pid):
    with open('/proc/stat') as f:
        fields=[int(x) for x in f.readline().split()[1:]]
    idle=fields[3]+fields[4]
    total=sum(fields)
    with open(f'/proc/{pid}/stat') as f:
        text=f.read()
    tail=text[text.rfind(')')+2:].split()
    ticks=int(tail[11])+int(tail[12])
    return total,idle,ticks

def run():
    arg=argparse.ArgumentParser()
    arg.add_argument('--minutes',type=int,default=60)
    arg.add_argument('--interval',type=int,default=60)
    args=arg.parse_args()
    if not 1<=args.minutes<=1440 or not 10<=args.interval<=300:
        raise SystemExit('duration or interval out of bounds')
    output=ROOT/'data/telemetry/v14-performance-hour.jsonl'
    output.parent.mkdir(parents=True,exist_ok=True)
    cutoff=time.monotonic()+args.minutes*60
    prev=None
    i=0
    while True:
        now=dt.datetime.now(dt.timezone.utc)
        row={'time':now.isoformat(),'sample':i}
        try:
            pid=int(subprocess.check_output(['systemctl','show','pscaper','-p','MainPID','--value'],text=True,timeout=4))
            row['pid']=pid
            total,idle,ticks=read_usage(pid)
            if prev and prev[0]==pid:
                cpu_fraction=(total-prev[1])
                if cpu_fraction>0:
                    row['vps_cpu_pct']=round(100*(1-(idle-prev[2])/cpu_fraction),2)
                    # /proc/stat sums all cores; this is the process share
                    # of the entire VPS, not percentage of one logical core.
                    row['pscaper_cpu_pct_vps']=round(100*(ticks-prev[3])/cpu_fraction,2)
            prev=(pid,total,idle,ticks)
            with open(f'/proc/{pid}/status') as f:
                for line in f:
                    if line.startswith('VmRSS:'):
                        row['rss_kib']=int(line.split()[1]);break
            with urllib.request.urlopen('http://127.0.0.1:8788/api/live',timeout=10) as response:
                obj=json.load(response)
            for key in ('alive','gold','total','queue','urgent_queue','discovery_queue','source_feeds',
                        'source_errors','validation_attempts','transient_recoveries','confirmed_losses'):
                row[key]=obj.get(key)
            row['publisher_state']=(obj.get('publishing') or {}).get('state')
        except Exception as e:
            row['error']=str(e)[:200]
        with output.open('a') as file:
            file.write(json.dumps(row,separators=(',',':'))+'\n')
        print(now.isoformat(),'sample',i,'alive',row.get('alive'),'queue',row.get('queue'),'error',row.get('error'),flush=True)
        i+=1
        if time.monotonic()>=cutoff:
            break
        time.sleep(min(args.interval,max(0,cutoff-time.monotonic())))
if __name__=='__main__':
    run()
