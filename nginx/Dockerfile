# Nginx Dockerfile with self-signed SSL certificates
# This creates a custom Nginx image with pre-generated self-signed certificates for development/testing

FROM nginx:alpine

# Copy custom Nginx configuration
COPY nginx.conf /etc/nginx/nginx.conf

# Copy entrypoint script
COPY docker-entrypoint.sh /docker-entrypoint.sh
RUN chmod +x /docker-entrypoint.sh

# Expose ports
EXPOSE 80 443

# Use custom entrypoint to generate certificates and start Nginx
ENTRYPOINT ["/docker-entrypoint.sh"]
