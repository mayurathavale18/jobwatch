import sqlite3, json, os, sys
from datetime import datetime

db_path = os.path.expanduser('~/jobwatch/jobwatch.db')
tailored_path = os.path.expanduser('~/jobwatch/resume/tailored.json')

conn = sqlite3.connect(db_path)
cur = conn.cursor()
cur.execute("SELECT id, company_name, title, url FROM jobs WHERE status='new'")
new_jobs = cur.fetchall()

tailored = {}
if os.path.exists(tailored_path):
    with open(tailored_path, 'r') as f:
        tailored = json.load(f)

today = datetime.now().strftime('%Y-%m-%d')
today_count = sum(1 for v in tailored.values() if v.get('tailored_date','').startswith(today) and v.get('status') == 'done')

untailored = [job for job in new_jobs if str(job[0]) not in tailored]

print(f"TOTAL_NEW_JOBS={len(new_jobs)}")
print(f"TODAY_TAILORED={today_count}")
print(f"UNTAILORED={len(untailored)}")
for job in untailored:
    print(f"JOB|{job[0]}|{job[1]}|{job[2]}|{job[3]}")
