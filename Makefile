.PHONY: create-bucket copy-object localrun createall

localrun:
	@AWS_SDK_LOAD_CONFIG=true BUILD_MODE=local ENVIRONMENT=local AWS_DEFAULT_REGION=ap-south-1 AWS_PROFILE=local DEBUG=true go run main.go local-bucket

create-bucket:
	@AWS_PROFILE=local AWS_DEFAULT_REGION=ap-south-1 aws --endpoint-url=http://localhost:4566 s3 mb s3://local-bucket --no-cli-pager

FILE ?= Makefile
PREFIX ?= ""
copy-object:
	@AWS_PROFILE=local AWS_DEFAULT_REGION=ap-south-1 aws --endpoint-url=http://localhost:4566 s3 cp ${FILE} s3://local-bucket/${PREFIX}${FILE}

createall: create-bucket copy-object
