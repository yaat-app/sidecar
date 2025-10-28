# Deployment Guide

This guide walks through deploying the YAAT sidecar to the public GitHub repository.

## Prerequisites

- Git installed
- GitHub CLI (`gh`) installed (optional, but recommended)
- Write access to https://github.com/yaat-app/sidecar

## Step 1: Clone the GitHub Repository

```bash
# Navigate to your projects directory
cd ~/Desktop/yaat/

# Clone the empty repository
git clone https://github.com/yaat-app/sidecar.git yaat-app-sidecar

# Enter the repository
cd yaat-app-sidecar
```

## Step 2: Copy Sidecar Code

```bash
# Copy all files from the local sidecar directory
cp -r ../sidecar/* .
cp -r ../sidecar/.github .

# Verify files copied correctly
ls -la
```

You should see:
- `.github/` directory with workflows
- `cmd/` directory
- `internal/` directory
- `go.mod`, `go.sum`
- `README.md`
- `yaat.yaml.example`
- `install.sh`
- `DEPLOYMENT.md` (this file)

## Step 3: Initialize Git and Push

```bash
# Check git status
git status

# Add all files
git add .

# Commit
git commit -m "Initial release: YAAT Sidecar v1.0.0

- Multi-platform support (Linux, macOS, Windows)
- HTTP proxy monitoring
- Log file tailing (Django, Nginx, JSON)
- Buffered event delivery
- Automated builds via GitHub Actions"

# Push to main branch
git push origin main
```

## Step 4: Create First Release

### Option A: Using GitHub CLI (Recommended)

```bash
# Create and push a tag
git tag v1.0.0
git push origin v1.0.0

# This will trigger the GitHub Actions workflow to build binaries automatically
# Wait 5-10 minutes for the workflow to complete
```

Check the build status:
```bash
gh run list
```

Once the build completes, the release will be created automatically with all platform binaries attached!

### Option B: Manual Release (via GitHub Web UI)

1. Go to https://github.com/yaat-app/sidecar/releases
2. Click "Draft a new release"
3. Click "Choose a tag" → type `v1.0.0` → "Create new tag: v1.0.0 on publish"
4. Title: `v1.0.0 - Initial Release`
5. Description:
   ```markdown
   ## 🎉 Initial Release

   YAAT Sidecar is a backend monitoring agent for the YAAT analytics platform.

   ### Features
   - ✅ HTTP traffic monitoring via reverse proxy
   - ✅ Real-time log file tailing
   - ✅ Support for Django, Nginx, and JSON logs
   - ✅ Zero code changes required
   - ✅ Multi-platform support

   ### Installation

   **Quick install:**
   ```bash
   curl -sSL https://raw.githubusercontent.com/yaat-app/sidecar/main/install.sh | bash
   ```

   **Manual install:** Download the binary for your platform below.

   ### Platform Support
   - Linux (amd64, arm64)
   - macOS (Intel, Apple Silicon)
   - Windows (amd64)

   See the [README](https://github.com/yaat-app/sidecar) for configuration and usage instructions.
   ```
6. Check "Set as the latest release"
7. Click "Publish release"

The GitHub Actions workflow will automatically build and attach binaries to this release!

## Step 5: Verify Installation Works

Test the installation script:

```bash
# In a temporary directory
cd /tmp

# Run the installer
curl -sSL https://raw.githubusercontent.com/yaat-app/sidecar/main/install.sh | bash

# Verify
yaat-sidecar --version
```

## Step 6: Update YAAT Dashboard

Update the API endpoint in your production YAAT instance:

1. Update `yaat.yaml.example` to use production API:
   ```yaml
   api_endpoint: "https://yaat.io/api/v1/ingest"
   ```

2. Ensure the dashboard links are correct:
   - Services page: Links to sidecar repo
   - API Keys page: Instructions reference the installation script

## Updating to New Versions

When you need to release a new version:

```bash
# Navigate to the repository
cd ~/Desktop/yaat/yaat-app-sidecar

# Pull latest changes from development
cp -r ../sidecar/* .

# Commit changes
git add .
git commit -m "Release v1.1.0: Add new features"

# Create and push new tag
git tag v1.1.0
git push origin main
git push origin v1.1.0
```

The GitHub Actions workflow will automatically build and release the new version!

## Troubleshooting

### GitHub Actions workflow not running

1. Check Actions are enabled: Settings → Actions → "Allow all actions"
2. Verify workflow file is at `.github/workflows/release.yml`
3. Check workflow runs: https://github.com/yaat-app/sidecar/actions

### Build failures

1. Check the Actions tab for error logs
2. Common issues:
   - Missing Go version (should be 1.21)
   - Missing dependencies (run `go mod tidy`)
   - Test failures (run `go test ./...` locally first)

### Installation script not working

1. Verify the script is accessible:
   ```bash
   curl https://raw.githubusercontent.com/yaat-app/sidecar/main/install.sh
   ```
2. Check file permissions in the repository (should be 644)
3. Ensure releases exist with properly named artifacts

## Support

If you encounter issues during deployment:

1. Check GitHub Actions logs
2. Verify repository permissions
3. Test locally before releasing
4. Contact the YAAT team for help
