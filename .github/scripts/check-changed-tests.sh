#!/bin/bash

error_handler() {
  local lineno=${BASH_LINENO[0]}
  local funcname=${FUNCNAME[1]} 
  local command=${BASH_COMMAND}

  # Print the error details. Please keep this for debugging purposes. 
  # echo "Error occurred at line $lineno in function $funcname: '$command'"
}

trap error_handler ERR

TARGET_BRANCH="main"
TEMP_DIR=$(mktemp -d)

touch "$TEMP_DIR/diff.used-anywhere"
touch "$TEMP_DIR/diff.test-functions"

# returns the name of a function, supplied as an argument
grep-for-function-name() {
    echo "$1" | grep func | \
    sed -E 's/.*func[[:space:]]+([a-zA-Z0-9_]+).*/\1/' | \
    grep -v "(" | \
    grep -v "//" # omit comments and functions that are part of a suite ()
}

# modifies diff.test-functions, adding a line-by-line list of where each supplied function is used, if it is used anywhere in go files.
find-all-functions-anywhere() {
    for function_name in $1 ; do
        contained_files=$(grep -n -r "$function_name" --exclude-dir=".git" --exclude-dir=".github" . | grep -v "//" | grep ".go" | grep -v "main.go" )
        echo "$contained_files" >> "$TEMP_DIR/diff.test-functions"
    done
}

grep-for-suite-and-test-name() {
    suite_name=$(echo "$1" | grep func | grep -oP '\((.*?)\)' | grep -oP '\*\K\w+' | grep -v "testing" | grep -v "github.com")
    test_name=$(echo "$1" | grep -oP 'func \(\w+\s\*\w+\) \K\w+')

    if [[ -n "$suite_name" && -n "$test_name" ]]; then
        echo "$suite_name $test_name"
    fi
}


# prepare for git diff to compare with latest main branch from origin. 
git fetch --all -q
git config user.name "github-actions"
git config user.email "github-actions@github.com"
git checkout "$1" -q
git rebase "origin/$TARGET_BRANCH" --strategy-option=theirs -q

# get all the modified test suites
git diff "origin/$TARGET_BRANCH" -- . ':(exclude)*.sh' ':(exclude)*.yml' | while read -r line; do
    grep-for-suite-and-test-name "$line" >> "$TEMP_DIR/diff.used-anywhere"
done

echo "\nTestSuites above were modified. TestSuites below use modified code from this PR.\n" >> "$TEMP_DIR/diff.used-anywhere"

# get all functions that changed (that aren't suites)
git diff "origin/$TARGET_BRANCH" -- . ':(exclude)*.sh' ':(exclude)*.yml' | while read -r line; do
    next=$(grep-for-function-name "$line")
    if [[ "$next" == *"Test"* ]]; then
        echo "$next" >> "$TEMP_DIR/diff.used-anywhere"
    else
        find-all-functions-anywhere "$next" 
    fi
done

# given changed functions, find tests that use said functions in any capacity and add them to diff.used-anywhere list
while IFS= read -r lines_not_tests; do
    if [[ "$lines_not_tests" != *"func"* && "$lines_not_tests" != *"TestSuite"* ]]; then
        if [[ "$lines_not_tests" == *".go"* ]]; then
        
            line_number=$(echo "$lines_not_tests" | awk -F":" '{print $2}')
            file=$(echo "$lines_not_tests" | awk -F":" '{print $1}')

            # sometimes, git diff has an = sign at the beginning of the string..
            if [[ ${file:0:1} == "=" ]]; then
                file="${file:1}"
            fi

            function_line=$(head -n "$line_number" "$file" | tac | grep -m 1 '^func' | tac)
            function_name_tmp=$(grep-for-function-name "$function_line")
            
            if [ -n "$function_name_tmp" ]; then
                find-all-functions-anywhere "$function_name_tmp"
            fi

            test_name=$(grep-for-suite-and-test-name "$function_line")
            if ! grep -q "$test_name" "$TEMP_DIR/diff.used-anywhere"; then
                echo "$test_name" >> "$TEMP_DIR/diff.used-anywhere"
            fi
        fi
    fi
done < "$TEMP_DIR/diff.test-functions"

wait

# curl needs non-escaped newlines to read properly into json
curl_digestable_string=""
while IFS= read -r official_tests; do
    curl_digestable_string+=$"\n$official_tests"
done < "$TEMP_DIR/diff.used-anywhere"

echo "$curl_digestable_string"

# Clean up
rm -rf "$TEMP_DIR"
