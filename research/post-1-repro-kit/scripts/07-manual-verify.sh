#!/bin/bash
# DF-4 phase 5: dump real file content for the 20 stratified repos so a human can
# confirm/refute the grep verdict on field d (and c).
D="${DF4_DIR:?set DF4_DIR to the df4 working directory (see README)}"
while IFS=$'\t' read -r vendor dv repo wraw dlogin dcfg; do
  slug=$(echo "$repo" | tr / _); dir="$D/clones/$slug"
  echo "################ $repo  [$vendor]  grep: d_code=$dv dlogin=$dlogin d_cfg=$dcfg"
  [ -d "$dir" ] || { echo "  MISSING CLONE"; continue; }
  echo "-- config(s):"
  find "$dir" -maxdepth 3 \( -name 'playwright*.config.*' -o -name 'cypress.config.*' -o -name 'cypress.json' \) \
    -not -path '*/node_modules/*' 2>/dev/null | head -3 | while read -r f; do
      echo "   [${f#$dir/}]"
      grep -nE 'workers|fullyParallel|storageState|globalSetup|dependencies:|testMatch|name:' "$f" | head -14 | sed 's/^/     /'
    done
  echo "-- storageState / globalSetup / cy.session in CODE (not .md):"
  grep -rn --exclude-dir=node_modules --exclude-dir=.git --include='*.ts' --include='*.tsx' --include='*.js' --include='*.jsx' --include='*.mjs' \
     -e 'storageState' -e 'globalSetup' -e 'cy\.session(' "$dir" 2>/dev/null | sed "s|$dir/||" | head -8 | sed 's/^/     /'
  echo "-- setup/auth files:"
  find "$dir" -not -path '*/node_modules/*' \( -name '*.setup.ts' -o -name 'global-setup.*' -o -name 'auth.setup.*' -o -name '*login*.ts' \) 2>/dev/null | sed "s|$dir/||" | head -5 | sed 's/^/     /'
  echo
done < "$D/manual20.tsv"
