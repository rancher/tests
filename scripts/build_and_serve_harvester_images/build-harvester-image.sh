#!/bin/bash

cd harvester

git checkout v1.7
git fetch

before=$(git rev-parse HEAD)
output=$(git rebase origin/v1.7 2>&1)
after=$(git rev-parse HEAD)

if [[ "$before" == "$after" ]]; then
    exit 0
fi

echo "$output"

docker system prune -f
docker volume prune -f

cd dist/artifacts/

rm -rf old/
rm -rf latest/

cd ../../

# can run make before-hand, but I don't think its necessary if I just want images. . 
# make 
make build-iso
sleep 10

cd dist/artifacts/

mkdir latest
mv harvester-* latest
cd latest

for file in *master*; do
    mv "$file" "${file//master/latest}"
done

cd ..
mv "image-lists-amd64.tar.gz" latest/

