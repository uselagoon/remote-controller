#!/bin/bash
REPO=${2:-uselagoon}
TAG=${1:-latest}
IMGNAME=${3:-remote-controller}
echo "Creating image for $REPO/${IMGNAME}:$TAG and pushing to docker hub"
make IMG=$REPO/${IMGNAME}:$TAG docker-build && make IMG=$REPO/${IMGNAME}:$TAG docker-push
