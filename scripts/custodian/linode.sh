#!/bin/bash

if ! command -v linode-cli &> /dev/null; then
  echo "linode-cli not found, installing..."
  pip3 install linode-cli --upgrade --break-system-packages
fi

if [ ! -f "$HOME/.config/linode-cli" ]; then
  mkdir -p $HOME/.config
  echo "token = $RANCHER_LINODE_ACCESSKEY" >> linode-config.txt
  cp linode-config.txt $HOME/.config/linode-cli
fi

linode-remove-old-instances()
{
  linode-list-old-instances > .linodelist
  while read id;
  do
    if [[ $id =$HOME ^[0-9] ]]; then
      echo $(($id)) &&  linode-cli linodes delete $(($id)) &
    fi
  done < ".linodelist";
  wait
}

export now=$(date +%s)
export cutoff=$((now - 2 * 24 * 3600))
export ddk=$(printf '%s' "$DONOTDELETE_KEYS" | jq -R 'split("|")')

linode-list-old-instances()
{
  linode-cli linodes list --json --all-columns --no-headers | jq --argjson cutoff "$cutoff" --argjson ddk "$ddk" '
    .[]
    | select(
        (.created | strptime("%Y-%m-%dT%H:%M:%S") | mktime) < $cutoff
        and
        ((.tags // []) | all(. as $tag | ($ddk | index($tag) | not)))
      )
    | .id
'
}

linode-remove-old-instances