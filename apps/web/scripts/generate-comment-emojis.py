"""Generate pinned comment emoji data from Unicode's emoji-test.txt.

Usage: python3 scripts/generate-comment-emojis.py /path/to/emoji-test.txt
Source: https://unicode.org/Public/emoji/latest/emoji-test.txt
License: https://www.unicode.org/license.txt
"""
import json
import pathlib
import re
import sys

repo = pathlib.Path(__file__).resolve().parents[3]
source = pathlib.Path(sys.argv[1]).read_text()
version = re.search(r"^# Version: (.+)$", source, re.M).group(1)
group = ""
items = []
for line in source.splitlines():
    if line.startswith("# group:"):
        group = line.removeprefix("# group:").strip()
    elif "; fully-qualified" in line:
        codepoints, _, description = line.partition(";")
        emoji = "".join(chr(int(cp, 16)) for cp in codepoints.split())
        name = re.split(r" E[\d.]+ ", description.partition("#")[2], maxsplit=1)[1]
        items.append([emoji, group, name.strip()])

api_dir = repo / "apps/api/internal/commentemoji"
web_dir = repo / "apps/web/src/comments"
api_dir.mkdir(parents=True, exist_ok=True)
web_dir.mkdir(parents=True, exist_ok=True)
header = f"# Unicode Emoji {version}. Copyright Unicode, Inc.\n# Source: https://unicode.org/Public/emoji/latest/emoji-test.txt\n# License: https://www.unicode.org/license.txt\n"
(api_dir / "sequences.txt").write_text(header + "\n".join(f"{emoji}\t{name}" for emoji, _, name in items) + "\n")
(web_dir / "emoji-data.json").write_text(json.dumps({"version": version, "source": "https://unicode.org/Public/emoji/latest/emoji-test.txt", "license": "https://www.unicode.org/license.txt", "items": items}, ensure_ascii=False, separators=(",", ":")) + "\n")
print(f"Generated Unicode Emoji {version}: {len(items)} sequences")
