import sqlite3, json, re, sys, os

def strip_html(html):
    if not html:
        return ''
    text = re.sub(r'<script.*?</script>', '', html, flags=re.S|re.I)
    text = re.sub(r'<style.*?</style>', '', text, flags=re.S|re.I)
    text = re.sub(r'<[^>]+>', ' ', text)
    text = re.sub(r'\s+', ' ', text).strip()
    return text

def get_description(raw):
    if not raw:
        return None
    try:
        data = json.loads(raw)
    except Exception as e:
        return f"[JSON parse error: {e}]"
    
    # Known paths
    for path in ['descriptionPlain', 'descriptionHtml', 'content', 'description']:
        val = data.get(path)
        if val and isinstance(val, str):
            if '<' in val and '>' in val:
                return strip_html(val)
            return val
    
    # Fallback: any field containing description or content
    for k, v in data.items():
        if isinstance(v, str) and ('description' in k.lower() or 'content' in k.lower()):
            if '<' in v and '>' in v:
                return strip_html(v)
            return v
    
    return "[No description found]"

job_id = sys.argv[1]
conn = sqlite3.connect(os.path.expanduser('~/jobwatch/jobwatch.db'))
raw = conn.execute("SELECT raw FROM jobs WHERE id=?", (job_id,)).fetchone()[0]
desc = get_description(raw)
print(desc)
