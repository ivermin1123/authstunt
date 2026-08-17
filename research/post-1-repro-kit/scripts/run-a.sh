#!/bin/bash
# DF-4 phase 3: clone qualified repos shallow, measure 9 fields with path:line
D="${DF4_DIR:?set DF4_DIR to the df4 working directory (see README)}"
C=$D/clones; E=$D/evidence; mkdir -p "$C" "$E"
: > "$D/fields-a.tsv"

G() { grep -rnIE --exclude-dir=.git --exclude-dir=node_modules --exclude-dir=dist \
      --exclude-dir=build --exclude-dir=.next --exclude-dir=vendor "$@" 2>/dev/null; }

while IFS=$'\t' read -r repo tier vendors stars pushed branch cfgs; do
  slug=$(echo "$repo" | tr / _)
  dir="$C/$slug"
  if [ ! -d "$dir" ]; then
    git clone --depth 1 --quiet --single-branch "https://github.com/$repo.git" "$dir" 2>/dev/null || {
      echo "CLONEFAIL $repo" >&2; continue; }
  fi
  cd "$dir" || continue
  ev="$E/$slug.txt"; : > "$ev"

  # ---- config files actually present
  CFG=$(find . -maxdepth 4 \( -path ./node_modules -o -path ./.git \) -prune -o \
        -type f \( -name 'playwright*.config.*' -o -name 'cypress.config.*' -o -name 'cypress.json' \) -print 2>/dev/null | sed 's|^\./||')
  echo "== CONFIGS ==" >> "$ev"; echo "$CFG" >> "$ev"
  fw=""
  echo "$CFG" | grep -qi playwright && fw="playwright"
  echo "$CFG" | grep -qi cypress && fw="${fw:+$fw+}cypress"

  # ---- (c) workers / fullyParallel  [config files only, verbatim]
  echo "== C workers/fullyParallel ==" >> "$ev"
  wlines=""
  for f in $CFG; do
    o=$(grep -nE 'workers|fullyParallel|retries|testIsolation' "$f" 2>/dev/null | sed "s|^|$f:|")
    [ -n "$o" ] && { echo "$o" >> "$ev"; wlines="$wlines$o"$'\n'; }
  done
  # serial-mode declarations anywhere in tests
  serdecl=$(G -e "describe\.configure\(\s*\{\s*mode:\s*['\"]serial" -e "mode:\s*['\"]serial['\"]" . | head -5)
  [ -n "$serdecl" ] && { echo "-- serial mode decl --" >> "$ev"; echo "$serdecl" >> "$ev"; }

  # ---- (d) DECISIVE: storageState / globalSetup / cy.session
  echo "== D storageState/globalSetup/cy.session ==" >> "$ev"
  d_ss=$(G -e 'storageState' . | head -12)
  d_gs=$(G -e 'globalSetup' . | head -8)
  d_sess=$(G -e 'cy\.session\(' . | head -8)
  d_dep=$(for f in $CFG; do grep -nE "dependencies:\s*\[|name:\s*['\"](setup|auth|global setup)" "$f" 2>/dev/null | sed "s|^|$f:|"; done)
  d_setupfile=$(find . -path ./node_modules -prune -o -type f \( -name '*.setup.ts' -o -name '*.setup.js' -o -name 'global-setup.*' -o -name 'globalSetup.*' -o -name 'auth.setup.*' \) -print 2>/dev/null | sed 's|^\./||' | head -8)
  { echo "-- storageState --"; echo "$d_ss"; echo "-- globalSetup --"; echo "$d_gs";
    echo "-- cy.session --"; echo "$d_sess"; echo "-- project deps/setup names --"; echo "$d_dep";
    echo "-- setup files --"; echo "$d_setupfile"; } >> "$ev"
  dflag=NO
  [ -n "$d_ss$d_gs$d_sess" ] && dflag=YES
  # does the setup step actually LOG IN (vs just declare a state file)?
  dlogin=NO
  if [ "$dflag" = YES ]; then
    lf=$(G -lE 'storageState|globalSetup|cy\.session\(' . | head -20)
    for f in $lf $d_setupfile; do
      [ -f "$f" ] || continue
      grep -qiE "signIn|sign_in|login|logIn|password|getByLabel\(.?(Email|Password)|fill\(" "$f" 2>/dev/null && { dlogin=YES; break; }
    done
  fi

  # ---- (e) backdoor
  echo "== E backdoor ==" >> "$ev"
  e_out=$(G -e '\+clerk_test' -e '424242' -e 'setupClerkTestingToken' -e 'clerkSetup' \
             -e 'FIREBASE_AUTH_EMULATOR_HOST' -e 'connectAuthEmulator' -e 'firebase emulators' \
             -e 'AUTH0_TEST_PASSWORD|grant_type.*password|password.*grant_type' \
             -e 'auth0-cypress|cypress-auth0|MAGIC_LINK_TOKEN' \
             -e 'testing.?token|TESTING_TOKEN' . | grep -viE 'node_modules|CHANGELOG|\.lock' | head -14)
  echo "$e_out" >> "$ev"
  eflag=NO; [ -n "$e_out" ] && eflag=YES
  ekind=$(echo "$e_out" | grep -oiE '\+clerk_test|424242|setupClerkTestingToken|clerkSetup|FIREBASE_AUTH_EMULATOR_HOST|connectAuthEmulator|grant_type|testing.?token' | sort -u | tr '\n' ';')

  # ---- (f) mail catcher
  echo "== F mailcatcher ==" >> "$ev"
  f_out=$(G -ie 'mailpit' -ie 'mailhog' -ie 'maildev' -ie 'inbucket' -ie 'mailosaur' -ie 'mailtrap' -ie 'ethereal.email' -ie '1025' . | grep -viE 'CHANGELOG|\.lock' | head -10)
  echo "$f_out" >> "$ev"
  fkind=$(echo "$f_out" | grep -oiE 'mailpit|mailhog|maildev|inbucket|mailosaur|mailtrap|ethereal' | tr 'A-Z' 'a-z' | sort -u | tr '\n' ';')

  # ---- (g) real signup/verify/OTP/magic-link test
  echo "== G signup/verify/OTP/magiclink in tests ==" >> "$ev"
  TDIR=$(find . -maxdepth 3 -path ./node_modules -prune -o -type d \( -name e2e -o -name tests -o -name test -o -name cypress -o -name __tests__ -o -name specs -o -name playwright \) -print 2>/dev/null | sed 's|^\./||' | head -6)
  g_out=""
  if [ -n "$TDIR" ]; then
    g_out=$(grep -rnIE --exclude-dir=node_modules -e 'sign.?up|signUp|register|createAccount|verif(y|ication).?code|otp|OTP|magic.?link|one.?time.?(code|password)|resetPassword|forgot.?password' $TDIR 2>/dev/null | grep -viE '\.snap:|\.json:' | head -12)
  fi
  echo "$g_out" >> "$ev"
  gflag=NO; [ -n "$g_out" ] && gflag=YES
  # ---- (R9) test-file counts
  ntest=0; nauth=0
  if [ -n "$TDIR" ]; then
    tf=$(find $TDIR -type f \( -name '*.spec.*' -o -name '*.test.*' -o -name '*.e2e.*' -o -name '*.cy.*' \) 2>/dev/null | grep -v node_modules)
    ntest=$(echo "$tf" | grep -c . )
    nauth=$(echo "$tf" | grep -icE 'login|signin|sign-in|signup|sign-up|auth|register|onboard' )
    if [ "$nauth" = "0" ] && [ -n "$tf" ]; then
      nauth=$(echo "$tf" | tr '\n' '\0' | xargs -0 grep -lIiE 'signIn|sign in|log ?in|signUp|sign up' 2>/dev/null | grep -c .)
    fi
  fi
  echo "== R9 ntest=$ntest nauth=$nauth ==" >> "$ev"

  # ---- (h) pain signals
  echo "== H pain ==" >> "$ev"
  h_ctx=""
  for f in $CFG; do
    n=$(grep -nE 'workers' "$f" | head -3 | cut -d: -f1)
    for ln in $n; do
      s=$((ln-3)); [ $s -lt 1 ] && s=1
      c=$(sed -n "${s},$((ln+2))p" "$f" | grep -E '//|/\*|\*|#' | grep -viE '^\s*\*/\s*$')
      [ -n "$c" ] && h_ctx="$h_ctx[$f:$ln] $c"$'\n'
    done
  done
  # commit history of the E2E config file itself (shallow clone has no log -> use API)
  h_log=""
  for f in $CFG; do
    h_log="$h_log$(gh api "repos/$repo/commits?path=$f&per_page=100" --jq '.[]|"\(.sha[0:7]) \(.commit.message|split("\n")[0])"' 2>/dev/null \
      | grep -iE 'flak|parallel|worker|race|serial|concurren|conflict|retry|retries' | sed "s|^|$f |" | head -6)"$'\n'
  done
  h_log=$(echo "$h_log" | grep -v '^$' | head -10)
  h_code=$(G -iE 'flaky|flakiness|race condition|conflict.*parallel|parallel.*conflict|shared (test )?(user|account)' . | grep -viE 'CHANGELOG|\.lock|node_modules' | head -8)
  { echo "-- comments near workers --"; echo "$h_ctx"; echo "-- commit msgs --"; echo "$h_log"; echo "-- code mentions --"; echo "$h_code"; } >> "$ev"
  hflag=NO; [ -n "$h_ctx$h_log" ] && hflag=YES

  # ---- verbatim workers value
  wraw=$(echo "$wlines" | grep -E 'workers' | head -2 | tr '\n' ' ' | sed 's/\t/ /g' | cut -c1-220)
  fpraw=$(echo "$wlines" | grep -E 'fullyParallel' | head -1 | sed 's/\t/ /g' | cut -c1-160)
  [ -z "$wraw" ] && wraw="ABSENT"
  [ -z "$fpraw" ] && fpraw="ABSENT"
  ddep=NO; echo "$d_dep" | grep -q "dependencies:" && ddep=YES
  [ -n "$serdecl" ] && wraw="$wraw ||SERIALDECL|| $(echo "$serdecl"|head -1|cut -c1-120)"

  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
    "$repo" "$tier" "$vendors" "$fw" "$wraw" "$fpraw" "$dflag" "$dlogin" "$ddep" "$eflag" "$ekind" "$fkind" "$gflag" "$hflag" "$ntest" "$nauth" "$stars" "$pushed" >> "$D/fields-a.tsv"
  cd "$D" || exit
done < "$D/part-a"
echo "MEASURED: $(wc -l < "$D/fields-a.tsv")" >&2
