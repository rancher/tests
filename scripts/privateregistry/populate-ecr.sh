#!/usr/bin/bash

AWS_ACCESS_KEY_ID="$(aws configure get aws_access_key_id)"
AWS_SECRET_ACCESS_KEY="$(aws configure get aws_secret_access_key)"
REGION="$(aws configure get region)"
USERNAME="AWS"
ECR="$1"
RANCHER_VERSION="$2"

loginECR() {
    echo -e "\nLogging into ECR..."
    aws ecr get-login-password --region ${REGION} | docker login --username "${USERNAME}" --password-stdin "${ECR}"
}

createCert() {
    echo -e "\nCreating a self-signed certificate..."
    mkdir -p certs
    openssl req -newkey rsa:4096 -nodes -sha256 -keyout certs/domain.key -addext "subjectAltName = DNS:${ECR}" -x509 -days 365 -out certs/domain.crt -subj "/C=US/ST=CA/L=SUSE/O=Dis/CN=${ECR}"

    echo -e "\nCopying the certificate to the /etc/docker/certs.d/${ECR} directory..."
    sudo mkdir -p /etc/docker/certs.d/"${ECR}"
    sudo cp certs/domain.crt /etc/docker/certs.d/"${ECR}"/ca.crt
}

createECRRepo() {
    echo -e "\nDownloading "${RANCHER_VERSION}" image list and scripts..."
    wget https://github.com/rancher/rancher/releases/download/"${RANCHER_VERSION}"/rancher-images.txt
    wget https://github.com/rancher/rancher/releases/download/"${RANCHER_VERSION}"/rancher-save-images.sh
    wget https://github.com/rancher/rancher/releases/download/"${RANCHER_VERSION}"/sha256sum.txt

    echo -e "\nVerifying downloaded release assets..."
    if ! grep " rancher-images.txt$" sha256sum.txt > /tmp/rancher-verify.sha256 || ! test -s /tmp/rancher-verify.sha256 || ! sha256sum -c /tmp/rancher-verify.sha256; then
        rm -f /tmp/rancher-verify.sha256
        echo "Checksum verification failed for rancher-images.txt"
        return 1
    fi
    rm -f /tmp/rancher-verify.sha256

    if ! grep " rancher-save-images.sh$" sha256sum.txt > /tmp/rancher-verify.sha256 || ! test -s /tmp/rancher-verify.sha256 || ! sha256sum -c /tmp/rancher-verify.sha256; then
        rm -f /tmp/rancher-verify.sha256
        echo "Checksum verification failed for rancher-save-images.sh"
        return 1
    fi
    rm -f /tmp/rancher-verify.sha256
    chmod +x rancher-save-images.sh

    echo -e "\nCutting the tags from the image names..."
    while read LINE; do
        echo ${LINE} | cut -d: -f1
    done < rancher-images.txt > rancher-images-no-tags.txt

    echo -e "\nCreating ECR repositories..."
    for IMAGE in $(cat rancher-images-no-tags.txt); do
        aws ecr create-repository --repository-name ${IMAGE}
    done

    rm -f sha256sum.txt
}

saveAndLoadImages() {
    echo -e "\nSaving the images..."
    ./rancher-save-images.sh --image-list ./rancher-images.txt

    echo -e "\nTagging the images..."
    for IMAGE in $(cat rancher-images.txt); do
        docker tag ${IMAGE} ${ECR}/${IMAGE}
    done

    echo -e "\nPushing the newly tagged images ECR..."
    for IMAGE in $(cat rancher-images.txt); do
        docker push ${ECR}/${IMAGE}
    done
}

usage() {
	cat << EOF

$(basename "$0")

This script will populate a private ECR with Rancher images. This script assumes you have the following
tools installed and configured on the system:

    * Docker
    * AWS CLI

When running the script, specify the ECR URI and the version of Rancher, prefixed with a leading 'v'.

USAGE: % ./$(basename "$0") [options]

OPTIONS:
	-h	-> Usage

EXAMPLES OF USAGE:

* Run script
	
	$ ./$(basename "$0") <ECR URI> v<Rancher version>

EOF
}

while getopts "h" opt; do
    case ${opt} in
        h)
            usage 
            exit 0;;
    esac
done

Main() {
    loginECR
    createCert
    createECRRepo
    saveAndLoadImages
}

Main "$@"