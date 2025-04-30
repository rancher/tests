# QA Cloud Custodian

QA custodian will clean up resources in AWS, GCR, and AZURE. Currently, we have automation configured for:
* AWS
  * instances
  * NLBs
  * EKS
  * ssh keys
* AZURE
  * VMs
  * AKS
  * Network Interfaces (NI)
  * NSG
* GCP
  * instances
  * GKE

## How It Works

This stack is fairly simple. Cleanup is exclusively using [Cloud Custodian](https://cloud-custodian.github.io/cloud-custodian/docs/quickstart/index.html) in a docker container. We run this on a recurring basis using our pre-existing Jenkins setup, with a cron to run the custodian 2x per day (once at the start of the workday, once at the end). 

We depend on Jenkins to have a reliable keystore with all keys available for this script to use. 

Custodian management is currently divided into separate yaml files based on the cloud it is working from, aws, azure, and gcp. 

### Resource Management 
New Hires - update our keystore in AWS `USER_KEYS` to include your user key. Use that key in ALL resources you create when using SUSE resources.  Keep it short (6 characters or less) unique to you, and recognizable by others (And, this is case sensitive!). For example, if your name was Jane Elaine Morrison, something like `jem` would suffice. Just make sure you can remember it, and others can know who's it is if they are on your team. . These resources will be deleted every 2 days

There is a separate keystore variable, `DONOTDELETE_KEYS`, which are values in either tags or names of the resource(s) that should never be deleted. 

**Note** that each entry should be separated by a pipe `|` in the values of a given key. All values are case-sensitive

Any resource that isn't matched to a value of one of the above keys will be removed within 24 hours of creation.

We tie these keys into the cloud custodian with a simple `sed` command in the dockerfile that replaces each of the above keys in the yaml files with the values of said keys. This decision was made to keep any sensitive data separate from the code. 


### Important Values In The Code
* [Dockerfile](./Dockerfile)
  * a `sed` command replaces the hardcoded key names with the values from said key. These are arguments sent to the dockerfile at build time
  * (AWS explicit) hardcoded regions are ones we use on a regular basis, and therefore what the custodian will check against
  * for google cloud, `google_credentials.json` is a file local to where you run docker build. Also, the google cloud project is hard coded to our QA project.
  * `debugging`, add the flag `--dry-run` to each line in the dockerfile that starts with `custodian run`. Otherwise, running the container will modify resources in your cloud!! Debug/dry-run mode will report the number of resources which would have been modified
* `.yaml` files are built around the instructions from the [How It Works](#how-it-works) documentation from cloud custodian, which often adds new support for time-based management of resources. 
* [Jenkinsfile](./Jenkinsfile), specifically the first docker build line, to see which variables are necessary to build the container. 



### Non-Implemented Features

#### Tag-To-Save
tag-to-save is a yaml that is setup to block resources from being deleted untl untag-to-delete runs. Management of runtime would be through jenkins:
* untag job runs at EOD friday to cleanup any resources that were tagged to be saved earlier in the week (no resouces should be saved over the weekend)
* tagged job would be ran manually, where user(s) would specify their key or resource(s) which would be tagged to be saved until Friday
however, no one has had any request to save anything beyond 2 days (or at least not voiced it in any channel) so this feature isn't implemented to save on complexity/potential issues until it is needed

#### Linode

Originally, this was a hack-week project where linode resource management was done via its cli (Cloud Custodian doesn't support linode) however CW ripped this out except for one part, the build args to the dockerfile. Doing resource management through the CLI would be difficult unless we used an api-key with org-level access to resources. Currently for QA, linode is the only cloud we use that is user-scoped correctly, meaning that my key doesn't have access to see other users resources in our org. Generally a good thing, but not for cleanup. So this is left as a placeholder until we want to manage linode resources too. This would be a separate effort, unless Cloud Custodian decides to add support for it. 


### Limitations

#### Cloud Custodian
We are limited by the tool chosen to manage cloud resources. Some limitations include:
* Linode and other clouds aren't supported
* unable to list reources modified, we only get a number of affected resources reported back by the custodian