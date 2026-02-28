# TLS Package REST APIs

This package provides REST APIs for managing TLS certificates and configuration.

## Base Path
`/tls`

## Certificate Management

### Add CA Certificate
- **PUT/POST** `/tls/cacert/add`
  - Body: Certificate data (PEM format)

### Remove CA Certificate
- **PUT/POST** `/tls/cacert/remove`

## Configuration

### Set Working Directory
- **POST/PUT** `/tls/workdir/set?dir={dir}`

## Notes
- `{dir}` - Directory path for TLS working files
- CA certificates should be provided in PEM format in the request body
