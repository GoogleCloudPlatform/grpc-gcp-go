### How to build and upload it to the registry

This is an example that the gRPC team uses internally, so you may need to
change `IMAGE_NAME` for your needs.

#### Build and upload new docker image

Update the `IMAGE_VERSION` to match the version of the image you want to use.

From the `e2e-examples/gcs` directory:

```
$ export IMAGE_NAME=us-docker.pkg.dev/grpc-testing/testing-images-public/grpc-gcp-go-gcs-benchmark
$ export IMAGE_VERSION=20250601.0
$ docker build -t $IMAGE_NAME:$IMAGE_VERSION -f docker/Dockerfile ../
$ docker push $IMAGE_NAME:$IMAGE_VERSION
$ docker tag $IMAGE_NAME:$IMAGE_VERSION $IMAGE_NAME:latest
$ docker push $IMAGE_NAME:latest
```
