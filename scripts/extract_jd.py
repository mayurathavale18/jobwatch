"""Print a captured job description without making network requests."""
import sqlite3
import sys
from pathlib import Path
from tailor_resume import jd_text_from_raw


def main(job_id):
    with sqlite3.connect(Path(__file__).resolve().parent.parent / "jobwatch.db") as conn:
        row = conn.execute("SELECT manual_jd_text, raw FROM jobs WHERE id=?", (job_id,)).fetchone()
    if row is None:
        raise SystemExit("Job not found")
    print((row[0] or "").strip() or jd_text_from_raw(row[1]) or "[No description found]")


if __name__ == "__main__":
    main(sys.argv[1])
