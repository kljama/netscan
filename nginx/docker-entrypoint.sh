#!/bin/sh
# Entrypoint script for Nginx with self-signed certificate generation

set -e

# Create SSL directory if it doesn't exist
mkdir -p /etc/nginx/ssl

# Generate self-signed certificate if it doesn't exist
if [ ! -f /etc/nginx/ssl/cert.pem ] || [ ! -f /etc/nginx/ssl/key.pem ]; then
    echo "Generating self-signed SSL certificate..."
    
    # Check if openssl is available, if not install it
    if ! command -v openssl >/dev/null 2>&1; then
        echo "Installing OpenSSL..."
        apk add --no-cache openssl 2>/dev/null || true
    fi
    
    # Generate the certificate
    openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
        -keyout /etc/nginx/ssl/key.pem \
        -out /etc/nginx/ssl/cert.pem \
        -subj "/C=US/ST=State/L=City/O=netscan/OU=Development/CN=localhost" \
        2>/dev/null || {
            echo "ERROR: Failed to generate SSL certificate"
            exit 1
        }
    
    echo "Self-signed SSL certificate generated successfully"
else
    echo "Using existing SSL certificate"
fi

# Start Nginx
echo "Starting Nginx..."
exec nginx -g "daemon off;"
