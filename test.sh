#!/bin/bash

pushd ./test/ || exit 1

go test -race ./...

popd || exit 1

pushd ./examples/ || exit 1

go run .

popd || exit 1
