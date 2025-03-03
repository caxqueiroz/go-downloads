GO_BUILD_ENV := CGO_ENABLED=0 GOOS=linux GOARCH=amd64
DOCKER_BUILD=$(shell pwd)/.docker_build
DOCKER_CMD=$(DOCKER_BUILD)/go-downloads
APP_NAME=go-downloads
S3_BUCKET=go-downloads-1
REGION=us-east-1

.PHONY: all build clean deploy setup-aws upload-test-files setup-heroku run-local

all: build deploy

$(DOCKER_CMD): clean
	mkdir -p $(DOCKER_BUILD)
	$(GO_BUILD_ENV) go build -v -o $(DOCKER_CMD) .

build: $(DOCKER_CMD)

# Clean build artifacts
clean:
	rm -rf $(DOCKER_BUILD)

# Deploy to Heroku
deploy: build
	heroku container:push web --app $(APP_NAME)
	heroku container:release web --app $(APP_NAME)

# Open the app in browser
open:
	heroku open --app $(APP_NAME)

# Setup AWS S3 bucket and IAM user
setup-aws:
	@echo "Creating S3 bucket and IAM user..."
	aws s3 mb s3://$(S3_BUCKET) --region $(REGION)
	aws iam create-user --user-name s3-download-app-user
	@echo "Creating access key..."
	aws iam create-access-key --user-name s3-download-app-user --output json > credentials.json
	@echo "Creating and attaching policy..."
	aws iam create-policy --policy-name S3DownloadAppPolicy --policy-document file://s3-policy.json
	AWS_ACCOUNT_ID=$$(aws sts get-caller-identity --query Account --output text) && \
	aws iam attach-user-policy --user-name s3-download-app-user --policy-arn arn:aws:iam::$$AWS_ACCOUNT_ID:policy/S3DownloadAppPolicy
	@echo "AWS setup complete. Credentials saved to credentials.json"

# Upload test files to S3
upload-test-files:
	@echo "Creating test files..."
	mkdir -p test-files
	echo "Hello World - Test File 1" > test-files/test1.txt
	echo "Hello World - Test File 2" > test-files/test2.txt
	@echo "Uploading test files to S3..."
	aws s3 cp test-files/ s3://$(S3_BUCKET)/ --recursive
	@echo "Verifying upload..."
	aws s3 ls s3://$(S3_BUCKET)/
	@echo "Test files uploaded successfully"

# Configure Heroku with environment variables
setup-heroku:
	@echo "Setting up Heroku environment variables..."
	heroku config:set S3_BUCKET_NAME=$(S3_BUCKET) --app $(APP_NAME)
	heroku config:set AWS_REGION=$(REGION) --app $(APP_NAME)
	@echo "Please enter your AWS Access Key ID:"
	@read ACCESS_KEY && heroku config:set AWS_ACCESS_KEY_ID=$$ACCESS_KEY --app $(APP_NAME)
	@echo "Please enter your AWS Secret Access Key:"
	@read SECRET_KEY && heroku config:set AWS_SECRET_ACCESS_KEY=$$SECRET_KEY --app $(APP_NAME)
	@echo "Heroku configuration complete"

# Run the application locally
run-local:
	export S3_BUCKET_NAME=$(S3_BUCKET) && \
	export AWS_REGION=$(REGION) && \
	export PORT=8080 && \
	go run main.go

# View application logs
logs:
	heroku logs --tail --app $(APP_NAME)

# Complete setup and deployment in one command
setup-all: setup-aws upload-test-files setup-heroku deploy open
