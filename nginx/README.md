# Nginx HTTPS Proxy for InfluxDB

This directory contains the Nginx reverse proxy configuration that provides secure HTTPS access to the InfluxDB web UI.

## Overview

The Nginx proxy service:
- **Terminates SSL/TLS** connections from browsers
- **Proxies requests** to the internal InfluxDB service (http://influxdb:8086)
- **Redirects HTTP to HTTPS** automatically
- **Generates self-signed certificates** at startup for local development/testing

## Files

- **`nginx.conf`** - Main Nginx configuration
  - HTTP (port 80): Redirects all traffic to HTTPS
  - HTTPS (port 443): Terminates SSL and proxies to InfluxDB
  
- **`Dockerfile`** - Custom Nginx image definition
  - Based on `nginx:alpine`
  - Includes entrypoint script for certificate generation
  
- **`docker-entrypoint.sh`** - Startup script
  - Generates self-signed SSL certificates if they don't exist
  - Starts Nginx in the foreground

## Security Notes

### Self-Signed Certificates (Development/Testing)

The service automatically generates self-signed SSL certificates at startup. These are suitable for:
- ✅ Local development
- ✅ Testing environments
- ✅ Isolated lab networks

Browser warnings are expected and safe to bypass when using self-signed certificates on localhost.

### Production Deployment

For production use, **replace the self-signed certificates** with proper CA-signed certificates:

1. Obtain certificates from a trusted Certificate Authority (Let's Encrypt, DigiCert, etc.)
2. Mount your certificates into the container:
   ```yaml
   volumes:
     - ./certs/cert.pem:/etc/nginx/ssl/cert.pem:ro
     - ./certs/key.pem:/etc/nginx/ssl/key.pem:ro
   ```
3. Update the `docker-entrypoint.sh` to skip certificate generation if certificates already exist (already implemented)

## Configuration

The default configuration:
- Listens on port **80** (HTTP) - redirects to HTTPS
- Listens on port **443** (HTTPS) - proxies to InfluxDB
- Proxies to **http://influxdb:8086** (internal Docker network)
- Enables WebSocket support for InfluxDB UI

To customize, edit `nginx.conf` and rebuild:
```bash
docker compose build nginx
docker compose up -d
```

## Troubleshooting

### Certificate Errors in Browser

**Expected behavior**: Browsers will show a security warning for self-signed certificates.

**To proceed**:
- Chrome/Edge: Click "Advanced" → "Proceed to localhost (unsafe)"
- Firefox: Click "Advanced" → "Accept the Risk and Continue"
- Safari: Click "Show Details" → "visit this website"

### Connection Refused

If you can't connect to https://localhost:

1. Check nginx container is running:
   ```bash
   docker compose ps nginx
   ```

2. Check nginx logs:
   ```bash
   docker compose logs nginx
   ```

3. Verify InfluxDB is accessible from nginx:
   ```bash
   docker compose exec nginx wget -qO- http://influxdb:8086/health
   ```

### Port Already in Use

If ports 80 or 443 are already in use on your host:

1. Stop conflicting services (Apache, other nginx instances, etc.)
2. Or change the exposed ports in `docker-compose.yml`:
   ```yaml
   ports:
     - "8080:80"    # HTTP
     - "8443:443"   # HTTPS
   ```
   Then access via https://localhost:8443
