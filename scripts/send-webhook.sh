#!/bin/bash

# Script to send a deployment webhook request
# Usage: ./scripts/send-webhook.sh [payload-file] [api-url] [deploy-token]
# Supports both YAML and JSON payload files

set -e

# Default values
PAYLOAD_FILE="${1:-webhook-payload.example.json}"
API_URL="${2:-http://localhost:7082}"
DEPLOY_TOKEN="${3:-${DEPLOY_TOKEN:-your-deploy-token-here}}"
ENDPOINT="${API_URL}/api/webhook/deploy"

# Colors
GREEN='\033[0;32m'
BLUE='\033[0;34m'
RED='\033[0;31m'
NC='\033[0m'

echo -e "${GREEN}Sending webhook request...${NC}"
echo -e "  Payload file: ${BLUE}${PAYLOAD_FILE}${NC}"
echo -e "  Endpoint: ${BLUE}${ENDPOINT}${NC}"

# Check if payload file exists
if [ ! -f "$PAYLOAD_FILE" ]; then
    echo -e "${RED}Error: Payload file not found: ${PAYLOAD_FILE}${NC}"
    exit 1
fi

# Determine file type and convert to JSON if needed
if [[ "$PAYLOAD_FILE" == *.yaml ]] || [[ "$PAYLOAD_FILE" == *.yml ]]; then
    # YAML file - need to convert to JSON
    if command -v yq &> /dev/null; then
        JSON_PAYLOAD=$(yq eval -o=json "$PAYLOAD_FILE")
    elif command -v python3 &> /dev/null; then
        # Fallback to Python if yq is not available
        JSON_PAYLOAD=$(python3 -c "
import yaml
import json
import sys
with open('$PAYLOAD_FILE', 'r') as f:
    data = yaml.safe_load(f)
    print(json.dumps(data))
")
    else
        echo -e "${RED}Error: YAML file requires either yq or Python3 with PyYAML.${NC}"
        echo -e "  Install yq: ${BLUE}brew install yq${NC}"
        echo -e "  Or install Python PyYAML: ${BLUE}pip3 install pyyaml${NC}"
        exit 1
    fi
elif [[ "$PAYLOAD_FILE" == *.json ]]; then
    # JSON file - use directly
    JSON_PAYLOAD=$(cat "$PAYLOAD_FILE")
else
    echo -e "${RED}Error: Unsupported file format. Use .yaml, .yml, or .json${NC}"
    exit 1
fi

# Check if API is reachable
if ! curl -s --connect-timeout 2 --max-time 5 "$API_URL" > /dev/null 2>&1; then
    echo -e "${RED}Error: Cannot connect to API at ${API_URL}${NC}"
    echo -e "  Make sure the API service is running: ${BLUE}make run-api${NC} or ${BLUE}docker compose up api${NC}"
    exit 1
fi

# Send request
RESPONSE=$(curl -s -w "\n%{http_code}" \
    -X POST \
    -H "Content-Type: application/json" \
    -H "x-deploy-token: ${DEPLOY_TOKEN}" \
    -d "$JSON_PAYLOAD" \
    "$ENDPOINT" 2>&1)

# Check if curl command failed
CURL_EXIT_CODE=$?
if [ $CURL_EXIT_CODE -ne 0 ]; then
    echo -e "${RED}✗ Request failed (curl exit code: ${CURL_EXIT_CODE})${NC}"
    echo -e "${RED}Error details:${NC}"
    echo "$RESPONSE"
    exit 1
fi

# Extract HTTP status code (last line)
HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
# Extract response body (all but last line)
RESPONSE_BODY=$(echo "$RESPONSE" | sed '$d')

# Check if HTTP_CODE is a valid number
if ! [[ "$HTTP_CODE" =~ ^[0-9]+$ ]]; then
    echo -e "${RED}✗ Request failed: Invalid response from server${NC}"
    echo -e "${RED}Response:${NC}"
    echo "$RESPONSE"
    exit 1
fi

# Check response
if [ "$HTTP_CODE" -ge 200 ] && [ "$HTTP_CODE" -lt 300 ]; then
    echo -e "${GREEN}✓ Request successful (HTTP ${HTTP_CODE})${NC}"
    echo -e "${BLUE}Response:${NC}"
    echo "$RESPONSE_BODY" | jq '.' 2>/dev/null || echo "$RESPONSE_BODY"
else
    echo -e "${RED}✗ Request failed (HTTP ${HTTP_CODE})${NC}"
    echo -e "${RED}Response:${NC}"
    echo "$RESPONSE_BODY"
    exit 1
fi
