#!/bin/bash
# InfluxDB initialization script to create the health bucket
# This script runs after the container starts and waits for InfluxDB to be ready

set -e

echo "Waiting for InfluxDB to be ready..."
until influx ping &>/dev/null; do
  echo "InfluxDB not ready yet, waiting..."
  sleep 2
done

echo "InfluxDB is ready. Checking for health bucket..."

# Check if health bucket exists
if influx bucket list --name health --org "${DOCKER_INFLUXDB_INIT_ORG}" --token "${DOCKER_INFLUXDB_INIT_ADMIN_TOKEN}" --json 2>/dev/null | grep -q '"name":"health"'; then
  echo "Health bucket already exists"
else
  echo "Creating health bucket..."
  influx bucket create \
    --name health \
    --org "${DOCKER_INFLUXDB_INIT_ORG}" \
    --token "${DOCKER_INFLUXDB_INIT_ADMIN_TOKEN}" \
    --retention "${DOCKER_INFLUXDB_INIT_RETENTION}"
  echo "Health bucket created successfully"
fi

# -------------------------------------------------
# Apply Dashboards
# -------------------------------------------------
echo "Applying dashboards from /templates..."

# Apply Netscan dashboard if it exists
if [ -f /templates/netscan.json ]; then
  echo "Applying 'Netscan' dashboard..."
  # Note: --force yes is required for non-interactive scripts to skip confirmation prompts.
  # Without this flag, influx apply will abort with "Error: aborted application of template"
  # even though the dry run succeeds, because it's waiting for user input.
  influx apply \
    --file /templates/netscan.json \
    --org "${DOCKER_INFLUXDB_INIT_ORG}" \
    --token "${DOCKER_INFLUXDB_INIT_ADMIN_TOKEN}" \
    --force yes
  echo "Netscan dashboard applied successfully"
else
  echo "WARNING: netscan.json not found in /templates, skipping..."
fi

# Apply InfluxDB Health dashboard if it exists
if [ -f /templates/influxdb_health.json ]; then
  echo "Applying 'InfluxDB Health' dashboard..."
  # Note: --force yes is required for non-interactive scripts to skip confirmation prompts.
  influx apply \
    --file /templates/influxdb_health.json \
    --org "${DOCKER_INFLUXDB_INIT_ORG}" \
    --token "${DOCKER_INFLUXDB_INIT_ADMIN_TOKEN}" \
    --force yes
  echo "InfluxDB Health dashboard applied successfully"
else
  echo "WARNING: influxdb_health.json not found in /templates, skipping..."
fi

# Apply InfluxDB Operational Monitoring dashboard
if [ -f /templates/influxdb_operational_monitoring.yml ]; then
  echo "Applying 'InfluxDB 2.0 Operational Monitoring' dashboard..."
  # Note: --force yes is required for non-interactive scripts to skip confirmation prompts.
  influx apply \
    --file /templates/influxdb_operational_monitoring.yml \
    --org "${DOCKER_INFLUXDB_INIT_ORG}" \
    --token "${DOCKER_INFLUXDB_INIT_ADMIN_TOKEN}" \
    --force yes
  echo "InfluxDB Operational Monitoring dashboard applied successfully"
else
  echo "WARNING: influxdb_operational_monitoring.yml not found in /templates, skipping..."
fi

echo "Dashboard provisioning complete."
