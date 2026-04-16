#!/bin/bash

# Bypass proxies for local tests
export no_proxy=localhost,127.0.0.1

# Build the project
go build -o server main.go

# Start the server in the background
./server &
SERVER_PID=$!

# Wait for the server to start
sleep 2

# Test /ping endpoint
echo "Testing /ping..."
RESPONSE=$(curl -s http://localhost:8081/ping)
if [ "$RESPONSE" == '{"message":"pong"}' ]; then
    echo "✅ /ping passed"
else
    echo "❌ /ping failed: $RESPONSE"
    kill $SERVER_PID
    exit 1
fi

# Test /swagger endpoint
echo "Testing /swagger..."
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8081/swagger/index.html)
if [ "$HTTP_CODE" == "200" ]; then
    echo "✅ /swagger passed"
else
    echo "❌ /swagger failed: HTTP $HTTP_CODE"
    kill $SERVER_PID
    exit 1
fi

# Cleanup
kill $SERVER_PID
rm server
echo "All tests passed successfully!"
