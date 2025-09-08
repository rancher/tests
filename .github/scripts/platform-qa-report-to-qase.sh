#!/bin/bash
set -e

RESULTS_JSON="${1:-}"
PACKAGE_NAME="${2:-}"
QASE_TEST_RUN_ID="${3:-}"
QASE_AUTOMATION_TOKEN="${4:-}"

if [ -z "$RESULTS_JSON" ] || [ -z "$PACKAGE_NAME" ] || [ -z "$QASE_TEST_RUN_ID" ] || [ -z "$QASE_AUTOMATION_TOKEN" ]; then
  echo "Usage: $0 <results_json> <package_name> <qase_test_run_id> <qase_automation_token>"
  exit 1
fi

if [ -z "$GITHUB_WORKSPACE" ]; then
  echo "❌ GITHUB_WORKSPACE must be set"
  exit 1
fi

echo "📤 Reporting test results to Qase for package: $PACKAGE_NAME"
RESULTS_DIR=$(mktemp -d)
trap 'rm -rf "$RESULTS_DIR"' EXIT

PACKAGE_SAFE=$(echo "$PACKAGE_NAME" | tr '/' '_')
PACKAGE_RESULTS_JSON="$RESULTS_DIR/results_${PACKAGE_SAFE}.json"
cp "$RESULTS_JSON" "$PACKAGE_RESULTS_JSON"

REPORTER_SCRIPT="${GITHUB_WORKSPACE}/validation/pipeline/scripts/build_qase_reporter.sh"
REPORTER_BINARY="${GITHUB_WORKSPACE}/validation/reporter"

chmod +x "$REPORTER_SCRIPT"
"$REPORTER_SCRIPT" || { echo "❌ Failed to build Qase reporter"; exit 1; }

if [ ! -f "$REPORTER_BINARY" ]; then
  echo "❌ Reporter binary not found at $REPORTER_BINARY"
  exit 1
fi

cp "$PACKAGE_RESULTS_JSON" "$RESULTS_DIR/results.json"
cd "$RESULTS_DIR"
chmod +x "$REPORTER_BINARY"
export QASE_TEST_RUN_ID QASE_AUTOMATION_TOKEN
"$REPORTER_BINARY" --results results.json

rm -f "${GITHUB_WORKSPACE}/results.xml" "${GITHUB_WORKSPACE}/results.json"
rm -rf "$RESULTS_DIR"

echo "✅ Test Results have been published to Qase for package: $PACKAGE_NAME"
