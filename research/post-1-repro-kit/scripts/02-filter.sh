#!/bin/bash
# DF-4 phase 2: apply PREREG R2/R3 to candidates -> qualified repo list
D="${DF4_DIR:?set DF4_DIR to the df4 working directory (see README)}"
CUT=2025-02-16
VENDOR_RE='^(clerk|auth0|auth0-samples|auth0-developer-hub|firebase|googleapis|google|workos|workos-samples|stytchauth|stytch-labs|descope|descope-sample-apps|descopeinc|kinde-oss|kinde-starter-kits|supertokens|supabase|supabase-community)/'

: > "$D/meta.tsv"
: > "$D/qualified.tsv"
: > "$D/rejected.tsv"

total=$(wc -l < "$D/repos-uniq.txt"); i=0
while read -r repo; do
  i=$((i+1))
  [ $((i % 50)) -eq 0 ] && echo "meta $i/$total" >&2
  m=$(gh api "repos/$repo" --jq '[(.fork|tostring),(.archived|tostring),.pushed_at,(.stargazers_count|tostring),.default_branch,(.size|tostring)]|join("\t")' 2>/dev/null)
  if [ -z "$m" ]; then printf '%s\tGONE\n' "$repo" >> "$D/rejected.tsv"; continue; fi
  IFS=$'\t' read -r fork arch pushed stars branch size <<< "$m"
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$repo" "$fork" "$arch" "$pushed" "$stars" "$branch" "$size" >> "$D/meta.tsv"
  [ "$fork" = "true" ]  && { printf '%s\tFORK\n' "$repo" >> "$D/rejected.tsv"; continue; }
  [ "$arch" = "true" ]  && { printf '%s\tARCHIVED\n' "$repo" >> "$D/rejected.tsv"; continue; }
  [[ "${pushed:0:10}" < "$CUT" ]] && { printf '%s\tDEAD\t%s\n' "$repo" "$pushed" >> "$D/rejected.tsv"; continue; }

  tree=$(gh api "repos/$repo/git/trees/$branch?recursive=1" --jq '.tree[]|select(.type=="blob")|.path' 2>/dev/null)
  [ -z "$tree" ] && { printf '%s\tNOTREE\n' "$repo" >> "$D/rejected.tsv"; continue; }
  cfg=$(echo "$tree" | grep -Ei '(^|/)(playwright[^/]*\.config\.[cm]?[jt]s|cypress\.config\.[cm]?[jt]s|cypress\.json)$' | head -3 | tr '\n' ',')
  [ -z "$cfg" ] && { printf '%s\tNOCONFIG\n' "$repo" >> "$D/rejected.tsv"; continue; }

  tier=team
  [[ "$repo" =~ $VENDOR_RE ]] && tier=vendor
  vendors=$(awk -F"\t" -v r="$repo" '$1==r{print $2}' "$D/candidates.tsv" | sort -u | tr '\n' '+' | sed 's/+$//')
  echo "$tree" > "$D/trees/$(echo "$repo"|tr / _).txt"
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$repo" "$tier" "$vendors" "$stars" "${pushed:0:10}" "$branch" "$cfg" >> "$D/qualified.tsv"
done < "$D/frame.txt"
echo "QUALIFIED: $(wc -l < "$D/qualified.tsv")  REJECTED: $(wc -l < "$D/rejected.tsv")" >&2
