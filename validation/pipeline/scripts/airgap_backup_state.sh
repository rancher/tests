#!/bin/bash
set -e

if [ -f terraform.tfstate ]; then
    cp terraform.tfstate ${BACKUP_NAME}
    echo "SUCCESS: Immediate backup created: ${BACKUP_NAME}"
elif [ -f terraform-state.tfstate ]; then
    cp terraform-state.tfstate ${BACKUP_NAME}
    echo "SUCCESS: Immediate backup created from fallback: ${BACKUP_NAME}"
else
    echo "WARNING: No state file found for immediate backup"
fi