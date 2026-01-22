# Build and Serve -- Harvester Development Builds

## Prerequisites

* a machine that:
  * has at least 100GB space
  * has nginx installed/accessible

## Setup

### Dev for Harvester Builds

Everything should be included in the `setup-harvester-dev-build.sh` script. Simply copy it to your machine, then `chmod 777` the file, and run it.

## Building Harvester

Again, everything should be included in the script. The script expects a small disk, so it wipes the built items from the last run each time.
Copy over the file `build-harvester-image.sh`, `chmod 777` and run it.
If you want this to run this regularly, you can setup a cron for it via `crontab -e`.

## Serving the ISOs

First, ensure nginx is running. `sudo systemctl status nginx`

We need to ensure nginx has permission on the file(s) we want to share. **Note** Not sure how necessary this is but you should run it anyways just in case.

```/bin/bash
sudo usermod -aG webfiles www-data
sudo chgrp -R webfiles /home/ubuntu/harvester
```

Next, add `files.conf` to nginx. It should live in this dir `/etc/nginx/conf.d/`. Update ports here if you'd like in `files.conf`.
Then, test the changes `sudo nginx -t` should say the configuration is valid.
Finally, you should be able to visit your node's IP address:port you selected. You should see a single folder called `latest` that contains the ISO amongst other things that were built.
