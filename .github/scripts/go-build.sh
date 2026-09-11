#!/bin/bash
set -e

oldPWD="$(pwd)"
max_retries=3
retry_delay=5

dirs=("./actions" "./" "./interoperability")

for dir in "${dirs[@]}"; do
    echo "Building $dir"
    cd "$dir"

    attempt=1
    while [ "$attempt" -le "$max_retries" ]; do
        if go build ./...; then
            break
        fi

        if [ "$attempt" -eq "$max_retries" ]; then
            echo "Build failed in $dir after $max_retries attempts"
            cd "$oldPWD"
            exit 1
        fi

        echo "Build failed in $dir (attempt $attempt/$max_retries). Retrying in ${retry_delay}s..."
        attempt=$((attempt + 1))
        sleep "$retry_delay"
    done

    cd "$oldPWD"
done
