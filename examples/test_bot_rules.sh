#!/bin/bash

# Security Test: Bot Protection Validation
# Prerequisite: ./http-server --bind-file examples/security_bot_protection.bind

echo "--- Layer 4 Security: Bot Detection Test ---"
echo ""

# 1. Test curl (Blocked)
echo "[1] Testing 'curl' (Should be blocked)"
curl -s -I http://127.0.0.1:9300/api/data | grep "HTTP/1.1 403"
if [ $? -eq 0 ]; then echo "✅ Correctly blocked curl (403)"; else echo "❌ Failed to block curl"; fi

# 2. Test Empty User-Agent (Challenge Redirect)
echo "[2] Testing 'Empty User-Agent' (Should redirect to /_waf/challenge)"
curl -s -I -A "" http://127.0.0.1:9300/premium/content | grep -E "HTTP/1.1 (302|303)"
if [ $? -eq 0 ]; then echo "✅ Correctly redirected empty UA (302/303)"; else echo "❌ Failed to redirect empty UA"; fi

# 3. Test Browser Simulation (Success)
echo "[3] Testing 'Real Browser' (Should pass)"
curl -s -o /dev/null -H "User-Agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.114 Safari/537.36" \
     -H "Accept-Language: en-US,en;q=0.9" \
     -w "%{http_code}" http://127.0.0.1:9300/api/data | grep "200"
if [ $? -eq 0 ]; then echo "✅ Correctly allowed browser traffic (200)"; else echo "❌ Failed to allow browser traffic"; fi

echo ""
echo "--- Tests Completed ---"
