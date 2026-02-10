#!/bin/bash
set -e

RESULTS_JSON="${1:-}"
PACKAGE_NAME="${2:-}"
QASE_TEST_RUN_ID="${3:-}"
QASE_AUTOMATION_TOKEN="${4:-}"

# Check required arguments
if [ -z "$RESULTS_JSON" ] || [ -z "$PACKAGE_NAME" ] || [ -z "$QASE_TEST_RUN_ID" ] || [ -z "$QASE_AUTOMATION_TOKEN" ]; then
  echo "Usage: $0 <results_json> <package_name> <qase_test_run_id> <qase_automation_token>"
  exit 1
fi

# Check that results file exists
if [ ! -f "$RESULTS_JSON" ]; then
  echo "⚠️ No results file found at $RESULTS_JSON. Skipping Qase reporting."
  exit 0
fi

: "${GITHUB_WORKSPACE:?GITHUB_WORKSPACE must be set}"

echo "📤 Reporting test results to Qase for package: $PACKAGE_NAME"
RESULTS_DIR=$(mktemp -d)
trap 'rm -rf "$RESULTS_DIR"' EXIT

PACKAGE_SAFE=$(echo "$PACKAGE_NAME" | tr '/' '_')
PACKAGE_RESULTS_JSON="$RESULTS_DIR/results_${PACKAGE_SAFE}.json"
cp "$RESULTS_JSON" "$PACKAGE_RESULTS_JSON"

# Remove skipped tests
if command -v jq >/dev/null 2>&1; then
  jq 'map(select(.Status != "skip" and .Status != "skipped"))' "$PACKAGE_RESULTS_JSON" > "$RESULTS_DIR/results.json"
else
  echo "❌ jq is required to filter skipped tests"
  exit 1
fi
TEST_COUNT=$(jq length "$RESULTS_DIR/results.json")
echo "📝 Reporting $TEST_COUNT tests to Qase"

# Build Qase reporter
REPORTER_SCRIPT="${GITHUB_WORKSPACE}/validation/pipeline/scripts/build_qase_reporter.sh"
REPORTER_BINARY="${GITHUB_WORKSPACE}/validation/reporter"

chmod +x "$REPORTER_SCRIPT"
"$REPORTER_SCRIPT" || { echo "❌ Failed to build Qase reporter"; exit 1; }

if [ ! -f "$REPORTER_BINARY" ]; then
  echo "❌ Reporter binary not found at $REPORTER_BINARY"
  exit 1
fi

# Run reporter
cd "$RESULTS_DIR"
chmod +x "$REPORTER_BINARY"
export QASE_TEST_RUN_ID QASE_AUTOMATION_TOKEN
"$REPORTER_BINARY" --results results.json

# Clean up
rm -f "${GITHUB_WORKSPACE}/results.xml" "${GITHUB_WORKSPACE}/results.json"

echo "✅ Test Results have been published to Qase (Run ID: $QASE_TEST_RUN_ID) for package: $PACKAGE_NAME"
