# Vultr staging

This setup uses one Ubuntu 24.04 VM, PostgreSQL 16 on a Docker volume, and one container each for the API, ingestion worker, and analysis worker. The API listens on localhost. GitHub connects over SSH automatically after tests pass on main. The photo picker accepts catalog metadata and returns a curation artifact.

## 1. Prepare the VM

Install Docker Engine and the Compose plugin using the [Ubuntu instructions](https://docs.docker.com/engine/install/ubuntu/#install-using-the-apt-repository). Allow inbound TCP 22 for your workstation and the GitHub-hosted deploy runner. Keep 8080 and 5432 private. Run the following as root on the new VM.

```sh
apt-get update
apt-get install -y git curl openssl jq
adduser --disabled-password --gecos '' deploy
usermod -aG docker deploy
sudo -iu deploy
git clone https://github.com/rubberduckkk/looomer.git ~/looomer
install -m 700 ~/looomer/deploy/staging/receive.sh ~/receive
install -d -m 700 ~/.ssh
umask 077
cat > ~/staging.env <<ENV
POSTGRES_PASSWORD=$(openssl rand -hex 32)
LOOOMER_API_TOKEN=$(openssl rand -hex 32)
LOOOMER_ROUTER_URL=https://openrouter.ai/api/v1
LOOOMER_ROUTER_API_KEY=YOUR_ROUTER_KEY
LOOOMER_MODEL=YOUR_ROUTER_MODEL_ID
ENV
chmod 600 ~/staging.env
nano ~/staging.env
```

Set the router key and copy the exact model ID from your router. OpenRouter is the default endpoint. Choose a model with streaming and tool calling for photo curation. You can switch to another gateway exposing the same Chat Completions contract by changing the URL, key, and model ID. Apply edits to the existing staging.env and rerun the staging workflow to recreate the analysis worker. [OpenRouter tool-capable models](https://openrouter.ai/models?supported_parameters=tools).

Generate this file once. PostgreSQL uses its password on first initialization. The deploy account has Docker access and should be treated as a host administrator.

## 2. Connect GitHub to the VM

On your workstation generate a dedicated key and set the staging environment secret using GitHub CLI.

```sh
ssh-keygen -t ed25519 -N '' -f ~/.ssh/looomer-staging-deploy
gh secret set STAGING_SSH_KEY --env staging --repo rubberduckkk/looomer < ~/.ssh/looomer-staging-deploy
cat ~/.ssh/looomer-staging-deploy.pub
```

On the VM, as deploy, paste that public key in the command below. The forced command accepts only a commit SHA matching the fetched main branch.

```sh
printf '%s\n' 'restrict,command="/home/deploy/receive" ssh-ed25519 REPLACE_WITH_PUBLIC_KEY_BODY' >> ~/.ssh/authorized_keys
chmod 600 ~/.ssh/authorized_keys
cat /etc/ssh/ssh_host_ed25519_key.pub
```

Read the host key through the Vultr console or an already verified SSH session. On your workstation set the hostname and the known-hosts line using the public host key from the VM. Use the same hostname or IPv4 address for both.

```sh
host=YOUR_VULTR_IP
gh variable set STAGING_HOST --env staging --repo rubberduckkk/looomer --body "$host"
printf '%s ssh-ed25519 %s\n' "$host" 'HOST_PUBLIC_KEY_BODY' | gh secret set STAGING_KNOWN_HOSTS --env staging --repo rubberduckkk/looomer
```

## 3. Deploy a release

Open GitHub Actions → Staging. Select the latest main run or use Run workflow on main. The test job uses a disposable PostgreSQL database. When tests pass, the deploy job starts automatically. Each push to main follows this path. A failed deployment shows its failing command in Actions.

## 4. Exercise the photo slice

On the VM as deploy, run the following in Bash. The requests use sample metadata and the analysis job calls the configured model.

```bash
set -a
source ~/staging.env
set +a
api=http://127.0.0.1:8080/v1
auth="Authorization: Bearer $LOOOMER_API_TOKEN"
post() { curl -fsS -H "$auth" -H 'Content-Type: application/json' -H "Idempotency-Key: $3" -d "$2" "$api/$1" | jq -er .job_id; }
job() { curl -fsS -H "$auth" "$api/jobs/$1" | jq .; }
ingest=$(post events '{"source":"photos","events":[{"occurred_at":"2026-01-01T12:00:00Z","item":{"id":"staging-photo-1","uri":"photo:staging-photo-1","media_type":"image/jpeg","metadata":{"photo_id":"staging-photo-1","filename":"harbor.jpg","width":1600,"height":900}}}]}' photo-smoke-ingest-v1)
job "$ingest"
```

Repeat the last command until status is completed. Then submit the curation brief and poll its job.

```bash
run=$(post runs '{"app":"photo-set-curation","version":"0.2.0","task":"Pick one opener and explain the choice using only the catalog metadata.","start":"2026-01-01T12:00:00Z","end":"2026-01-01T12:00:01Z"}' photo-smoke-run-v1)
job "$run"
```

Look for completed status and the selected photo ID in result.artifact.payload. For a failed job inspect its error, fix the cause, and use a fresh analysis idempotency key to retry. To inspect service logs on the VM run `docker logs --tail 100 looomer-staging-analysis-1` (substitute ingest or api as needed). From a workstation you can reach the API with `ssh -N -L 8080:127.0.0.1:8080 ADMIN@YOUR_VULTR_IP` using your ordinary admin login.

## Recovery

For an application rollback revert the faulty commit on main and let the resulting workflow deploy it. Deploys reuse the PostgreSQL volume. Take a database backup before schema changes and plan database recovery separately. Register changed manifest definitions with a new version.

## Core admin

From the machine running your browser, open an SSH tunnel with your ordinary VM login:

```sh
ssh -N -L 8081:127.0.0.1:8081 ADMIN@staging.looomer.com
```

Open http://localhost:8081 and keep the tunnel open. Access to the loopback port grants operator access, including submitting model-backed runs. The admin service supplies the API credential on the server. Select a submission time to inspect batch events, errors, or an analysis artifact and its input snapshot and trace. Refresh activity to update job status. Run an app previews the committed event count for its source and UTC window. Workers select inputs when execution starts.
