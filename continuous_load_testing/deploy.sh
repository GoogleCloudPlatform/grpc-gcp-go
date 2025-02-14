#!/bin/bash
WORKING_DIR=$(pwd)
ROOT_DIR=$(dirname $(dirname $(pwd)))
echo $WORKING_DIR
echo $ROOT_DIR

METHODS="HalfDuplexCall"

if [ ! -z "$1" ]; then
    METHODS=$1
    echo "Using custom methods: $METHODS"
else
    echo "Using default methods: $METHODS"
fi
export METHODS

go generate && go build
kubectl delete deployment client-go-manual
docker system prune -af
docker build --progress=plain --no-cache -t directpathgrpctesting-client-go-manual .
docker tag directpathgrpctesting-client-go-manual us-docker.pkg.dev/directpathgrpctesting-client/directpathgrpctesting-client/directpathgrpctesting-client-go-manual
gcloud artifacts docker images delete us-docker.pkg.dev/directpathgrpctesting-client/directpathgrpctesting-client/directpathgrpctesting-client-go-manual --delete-tags -q
docker push us-docker.pkg.dev/directpathgrpctesting-client/directpathgrpctesting-client/directpathgrpctesting-client-go-manual
gcloud container clusters get-credentials cluster-1 --region us-west1 --project directpathgrpctesting-client
kubectl apply -f client-go-manual.yaml


echo "Running the Go application with methods: $METHODS"
