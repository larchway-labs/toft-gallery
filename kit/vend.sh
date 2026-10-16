#!/usr/bin/env bash
# Copy the kit into every example that carries it, and say what moved.
#
# Copies rather than a package, deliberately: an example has to be a repository
# a person can read top to bottom and deploy in one click, and a shared
# dependency would make sixteen apps un-runnable the day this one moved. The
# cost of that choice is this script and the check under it.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
root="$(dirname "$here")/examples"

css="$here/toft-kit.css"
js="$here/toft-kit.js"
ico="$here/favicon.svg"

# example:dir:what  — what is any of  css js ico
targets="
fastapi-reconcile:static:css js ico
flask-hello:static:css js ico
marimo-runway:static:css ico
mcp-server:static:css ico
node-api:public:css js ico
orders-viewer:static:css js ico
plumber-stats:www:css js ico
python-report:static:css js ico
quarto-service-review:static:css ico
rmarkdown-quality:static:css
service-catalog:assets:css js ico
shiny-abtest:www:css ico
streamlit-explorer:static:css
support-review:static:css
nextjs-dashboard:app:css
"

while IFS=: read -r repo dir what; do
  [ -z "$repo" ] && continue
  dest="$root/$repo/$dir"
  [ -d "$dest" ] || { echo "missing $dest"; exit 1; }
  for w in $what; do
    case "$w" in
      css) cp "$css" "$dest/toft-kit.css" ;;
      js)  cp "$js"  "$dest/toft-kit.js" ;;
      ico) cp "$ico" "$dest/favicon.svg" ;;
    esac
  done
  echo "vended -> $repo/$dir ($what)"
done <<< "$targets"

# Two that keep the kit somewhere the loop cannot express.
cp "$js"  "$root/nextjs-dashboard/public/toft-kit.js"
cp "$ico" "$root/nextjs-dashboard/public/favicon.svg"
echo "vended -> nextjs-dashboard/public (js ico)"

# Stockroom's stylesheet is the kit followed by its own layer, so it is rebuilt
# rather than copied. The layer is everything after the marker line.
stock="$root/stockroom/static/app.css"
marker="   Stockroom — what this app adds to the house style."
if grep -qF "$marker" "$stock"; then
  awk -v m="$marker" 'index($0,m){found=1} found' "$stock" > /tmp/toft-stock-layer.$$
  { cat "$css"; echo; echo "/* ═══════════════════════════════════════════════════════════════════════════"; cat /tmp/toft-stock-layer.$$; } > "$stock"
  rm -f /tmp/toft-stock-layer.$$
  cp "$ico" "$root/stockroom/static/favicon.svg"
  echo "rebuilt -> stockroom/static/app.css (kit + layer)"
fi
