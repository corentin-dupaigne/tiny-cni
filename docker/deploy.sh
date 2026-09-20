#!/bin/sh
set -e

echo "Moving tinycni binary on the node"
cp ./tiny-cni /opt/cni/bin/tiny-cni

echo "Moving tinycni config on the node"
cp ./config-default.json /etc/cni/net.d/05-tinycni.conf


