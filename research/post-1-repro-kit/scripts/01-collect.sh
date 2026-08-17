#!/bin/bash
# DF-4 phase 1: collect candidate repos = package.json containing BOTH an IdP SDK AND an E2E runner
# Signal-based, NOT star-based. No sort qualifier is used anywhere.
D="${DF4_DIR:?set DF4_DIR to the df4 working directory (see README)}"
OUT=$D/candidates.tsv
: > "$OUT"

IDP=(
 "clerk|@clerk/nextjs"
 "clerk|@clerk/clerk-react"
 "clerk|@clerk/clerk-js"
 "clerk|@clerk/express"
 "clerk|@clerk/backend"
 "clerk|@clerk/astro"
 "auth0|@auth0/nextjs-auth0"
 "auth0|@auth0/auth0-react"
 "auth0|@auth0/auth0-spa-js"
 "auth0|@auth0/auth0-angular"
 "firebase|firebase"
 "workos|@workos-inc/node"
 "workos|@workos-inc/authkit-nextjs"
 "stytch|@stytch/nextjs"
 "stytch|@stytch/react"
 "stytch|@stytch/vanilla-js"
 "stytch|@stytch/node"
 "descope|@descope/react-sdk"
 "descope|@descope/nextjs-sdk"
 "descope|@descope/node-sdk"
 "kinde|@kinde-oss/kinde-auth-nextjs"
 "kinde|@kinde-oss/kinde-auth-react"
 "supertokens|supertokens-node"
 "supertokens|supertokens-auth-react"
 "supertokens|supertokens-web-js"
 "supabase|@supabase/supabase-js"
)
E2E=("@playwright/test" "cypress")

for entry in "${IDP[@]}"; do
  vendor="${entry%%|*}"; pkg="${entry#*|}"
  for e in "${E2E[@]}"; do
    for page in 1 2 3; do
      q="\"$pkg\" \"$e\" filename:package.json"
      res=$(gh api -X GET "search/code" -f q="$q" -f per_page=100 -f page=$page \
            --jq '.items[] | .repository.full_name' 2>/dev/null)
      if [ -z "$res" ]; then sleep 7; break; fi
      echo "$res" | while read -r r; do
        [ -n "$r" ] && printf '%s\t%s\t%s\n' "$r" "$vendor" "$e" >> "$OUT"
      done
      n=$(echo "$res" | wc -l | tr -d ' ')
      echo "[$vendor/$pkg + $e p$page] $n" >&2
      sleep 7
      [ "$n" -lt 100 ] && break
    done
  done
done
echo "DONE. raw rows: $(wc -l < "$OUT")" >&2
