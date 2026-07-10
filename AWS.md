# AWS Deployment

This repo currently has a small public test deployment on EC2 for browser and
WebSocket smoke testing.

## What Is Deployed

- Instance: `i-08106835cc5495abc`
- Region: `us-east-1`
- Instance type: `t4g.nano`
- AMI: latest Amazon Linux 2023 ARM64
- Public IP: `44.222.174.192`
- Public URL: `http://44.222.174.192:8080/`

## AWS Resources

- IAM credentials are taken from the local default AWS CLI profile.
- EC2 key pair: `zztmmo-test-key`
- Security group: `sg-0c69577d6d95dd937`
- VPC: default VPC in `us-east-1`
- Public subnet: `subnet-0a480c5a5af2b01e9`

## Network Policy

- TCP `22` is open only from the current workstation IP.
- TCP `8080` is open to `0.0.0.0/0` for browser testing.
- The server listens on `:8080`.

## Server Layout

The instance runs a single `systemd` service:

`/etc/systemd/system/zztmmo.service`

It starts the Go server with:

```bash
/opt/zztmmo/zzt-server -addr :8080 -world TOWN -web web/dist -help . -saves saves
```

Pressing **'W'** on the title screen allows players to select and load other ZZT worlds dynamically (supported only when no players are active in rooms).

Installed files live under:

```bash
/opt/zztmmo
```

That directory contains:

- `zzt-server` built for Linux ARM64
- `web/dist` built browser assets
- `.ZZT` world files:
  - `TOWN.ZZT` (Town of ZZT)
  - `CAVES.ZZT` (Caves of ZZT)
  - `CITY.ZZT` (City of ZZT)
  - `DUNGEONS.ZZT` (Dungeons of ZZT)
  - `RHYGAR2.ZZT` & `RHYGAR2X.ZZT` (Rhygar 2 Part 1 & 2)
  - `BURGERJ.ZZT` (Burger Joint)
  - `WARTORN.ZZT` & `WARTORNX.ZZT` (War-Torn Part 1 & 2)
  - `KUDZU.ZZT` (Kudzu)
  - `ESPFILE1.ZZT` through `ESPFILE4.ZZT` (Evil Sorcerer's Party Parts 1-4)
  - `BURGLAR1.ZZT` & `BURGLAR2.ZZT` (Burglar! Part 1 & 2)
  - `MERC.ZZT` (The Mercenary)
  - `MONSTER.ZZT` (Monster Zoo)
- `.HLP` help files
- `saves/`

## How It Was Deployed

1. Build the browser client locally.
2. Build the Go server for Linux ARM64.
3. Package the server binary, browser assets, ZZT world files, help files, and saves
   directory into a `deploy.tar.gz` bundle.
4. Copy the bundle to the EC2 instance using SCP.
5. Unpack it under `/opt/zztmmo`.
6. Restart the `zztmmo.service`.
7. Verify:
   - `GET /` serves the browser UI
   - `GET /api/worlds` returns the list of all available games

## Local Commands

### 1. Build client assets and backend binary
Build the web client:

```bash
# In engine/web
npm run build
```

Build the Linux ARM64 server binary:

```bash
# In engine/
GOOS=linux GOARCH=arm64 go build -o zzt-server ./cmd/zzt-server
```

### 2. Package the files into a deployment bundle
Create a tarball containing the compiled Go binary, CP437 help files, game files, and web assets:

```bash
# In engine/
tar -czf deploy.tar.gz zzt-server web/dist *.ZZT *.HLP
```

### 3. Upload the bundle to the EC2 Instance
Upload the bundle to the temporary directory on the server:

```bash
scp -i ~/.ssh/id_ed25519 deploy.tar.gz ec2-user@44.222.174.192:/tmp/deploy.tar.gz
```

### 4. SSH and Extract on the Server
Connect to the server, stop the service, set folder permissions, unpack the bundle, create the saves folder, and clean up:

```bash
ssh -i ~/.ssh/id_ed25519 ec2-user@44.222.174.192 "sudo systemctl stop zztmmo && sudo mkdir -p /opt/zztmmo && sudo chown -R ec2-user:ec2-user /opt/zztmmo && tar -xzf /tmp/deploy.tar.gz -C /opt/zztmmo && mkdir -p /opt/zztmmo/saves && rm -f /tmp/deploy.tar.gz"
```

### 5. Restart and Verify the Service
Restart the service on the host, inspect its status, and check the public API endpoints:

```bash
# Start and status check
ssh -i ~/.ssh/id_ed25519 ec2-user@44.222.174.192 "sudo systemctl start zztmmo && sudo systemctl status zztmmo"

# Local endpoint validation
curl -I http://44.222.174.192:8080/
curl -s http://44.222.174.192:8080/api/worlds
```

## Security Group Management

Workstation IP access is locked down. If your local public IP changes, authorize it on EC2:

```bash
# 1. Fetch current public IP
curl https://checkip.amazonaws.com

# 2. Authorize SSH on Port 22 in Security Group
aws ec2 authorize-security-group-ingress --group-id sg-0c69577d6d95dd937 --protocol tcp --port 22 --cidr <your-workstation-ip>/32
```

## Operational Notes

- This is a low-cost public test server, not a hardened production setup.
- SSH access is restricted to the workstation IP used during setup.
- Stop charges by terminating the instance when it is no longer needed:

```bash
aws ec2 terminate-instances --region us-east-1 --instance-ids i-08106835cc5495abc
```

