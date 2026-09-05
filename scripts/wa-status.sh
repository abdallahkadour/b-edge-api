#!/usr/bin/env bash
# Check whether Meta is actually delivering WhatsApp messages.
#
# WHY THIS EXISTS
#
# Twilio's console and its Senders API both report the sender as ONLINE while
# Meta refuses every message. Twilio's status only reflects ITS registration;
# Meta gates delivery separately and reports it nowhere except the error code
# on a real send. So the only honest status check is to send one and read back
# what happened.
#
#   63051  Meta has locked the sender/WABA  -> business verification pending
#   63016  outside the 24h session window   -> needs a template, or an inbound
#   63015  sandbox not joined               -> wrong sender configured
#   (none) + delivered                      -> working
#
# Usage: scripts/wa-status.sh [+E164]     (default: the test handset)

set -euo pipefail
cd "$(dirname "$0")/.."
set -a; . ./.env; set +a

TO="${1:-+966537051438}"

printf '%s\n' "--- $(date -u '+%F %H:%M:%SZ') --- to $TO"

sid=$(curl -sS -u "$TWILIO_ACCOUNT_SID:$TWILIO_AUTH_TOKEN" -X POST \
  "https://api.twilio.com/2010-04-01/Accounts/$TWILIO_ACCOUNT_SID/Messages.json" \
  --data-urlencode "To=whatsapp:$TO" \
  --data-urlencode "From=whatsapp:$TWILIO_WHATSAPP_FROM" \
  --data-urlencode "Body=B-Edge delivery check $(date -u +%H:%M)" \
  | python3 -c 'import sys,json;print(json.load(sys.stdin).get("sid",""))')

if [ -z "$sid" ]; then echo "rejected at submit"; exit 1; fi

for _ in 1 2 3 4 5 6 7 8; do
  read -r status err < <(curl -sS -u "$TWILIO_ACCOUNT_SID:$TWILIO_AUTH_TOKEN" \
    "https://api.twilio.com/2010-04-01/Accounts/$TWILIO_ACCOUNT_SID/Messages/$sid.json" \
    | python3 -c 'import sys,json;d=json.load(sys.stdin);print(d.get("status",""),d.get("error_code") or "-")')
  case "$status" in delivered|read|undelivered|failed) break;; esac
  sleep 4
done

echo "status=$status error=$err"
case "$err" in
  -)     echo "OK - Meta is delivering." ;;
  63051) echo "BLOCKED - Meta locked the WABA. Business verification required." ;;
  63016) echo "No open 24h window. Needs an approved template, or an inbound message first." ;;
  *)     echo "See https://www.twilio.com/docs/api/errors/$err" ;;
esac
