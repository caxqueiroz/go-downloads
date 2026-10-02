# S3 File Download Server

A Go application that serves as a download server for files stored in an AWS S3 bucket. The app lists files from a specified S3 bucket and provides a clean web interface for users to browse and download files.

## Features

- Lists files from an AWS S3 bucket
- Displays file name, size, and last modified date
- Provides direct download links for each file
- Responsive web interface
- RESTful API endpoint for programmatic access
- Optional password protection for accessing files

## Prerequisites

- Go 1.24 or later
- AWS account with S3 bucket
- AWS credentials configured

## Environment Variables

The application requires the following environment variables:

- `PORT`: The port on which the server will listen
- `S3_BUCKET_NAME`: The name of the S3 bucket containing the files
- `AWS_REGION`: The AWS region where the S3 bucket is located (defaults to "us-east-1" if not specified)
- `AWS_ACCESS_KEY_ID`: Your AWS access key ID
- `AWS_SECRET_ACCESS_KEY`: Your AWS secret access key
- `AWS_SESSION_TOKEN`: Session token when using temporary AWS credentials
- `APP_PASSWORD`: Optional password to restrict access to the application (if not set, no password protection is applied)
- `SESSION_SECRET`: Optional key used to sign session cookies (if not set, a random one is generated at startup and sessions reset on restart)
- `SESSION_COOKIE_SECURE`: Defaults to secure cookies requiring HTTPS; set to `false` only for local HTTP development
- `BASE_PATH`: Optional URL prefix such as `/downloads` when a reverse proxy strips that prefix before forwarding requests
- `S3_ENDPOINT`: Optional endpoint for an S3-compatible store such as SeaweedFS (e.g. `http://mac-mini-uno:8333`); enables path-style addressing

## Running Locally

```bash
# Set required environment variables
export PORT=8080
export S3_BUCKET_NAME=your-bucket-name
export AWS_REGION=us-east-1
export AWS_ACCESS_KEY_ID=your-access-key
export AWS_SECRET_ACCESS_KEY=your-secret-key
export APP_PASSWORD=your-password  # Optional, for password protection
export SESSION_COOKIE_SECURE=false  # Local HTTP development only

# Run the application
go run main.go
```

## API Endpoints

- `GET /login`: Login page (when password protection is enabled)
- `POST /login`: Process login form
- `GET /`: Web interface showing the list of files (protected when password is set)
- `GET /download/:filename`: Download a specific file (protected when password is set)
- `GET /api/files`: JSON API endpoint returning the list of files (protected when password is set)

## Deployment

This application can be deployed to Heroku:

```bash
# Create a new Heroku app
heroku create

# Set the required environment variables
heroku config:set S3_BUCKET_NAME=your-bucket-name
heroku config:set AWS_REGION=us-east-1
heroku config:set AWS_ACCESS_KEY_ID=your-access-key
heroku config:set AWS_SECRET_ACCESS_KEY=your-secret-key
heroku config:set APP_PASSWORD=your-password  # Optional, for password protection

# Deploy the application
git push heroku main
```
