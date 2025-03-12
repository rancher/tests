#!/bin/bash

set -e

if grep -q "github.com/rancher/shepherd =>" go.mod; then
    echo "Error: Forked version of rancher/shepherd detected in go.mod"
    exit 1
fi
if grep -q "github.com/rancher/shepherd =>" actions/go.mod; then
    echo "Error: Forked version of rancher/shepherd detected in actions/go.mod"
    exit 1
fi

if grep -q "github.com/rancher/rancher =>" go.mod; then
    echo "Error: Forked version of rancher/rancher detected in go.mod"
    exit 1
fi
if grep -q "github.com/rancher/rancher =>" actions/go.mod; then
    echo "Error: Forked version of rancher/rancher detected in actions/go.mod"
    exit 1
fi