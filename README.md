# server-cli

Easy CLI tool for servers managed by FlyWP.

## Installation

### Prerequisites

- [Docker](https://www.docker.com/get-started)
- [Docker Compose](https://docs.docker.com/compose/install/)

### Quick Install

You can easily install the `fly` CLI tool using the following command. This will download and run the `install.sh` script, which will automatically detect your operating system and architecture, download the latest release, and install it to `/usr/local/bin`:

```bash
curl -sL https://raw.githubusercontent.com/flywp/server-cli/main/install.sh | bash
```

### Manual Installation

If you prefer to manually download and install the binary, follow these steps:

1. Download the precompiled binaries from the [Releases](https://github.com/flywp/server-cli/releases) page. Choose the version suitable for your operating system and architecture.

#### Linux

1. Download the tarball:
    ```bash
    wget https://github.com/flywp/server-cli/releases/download/latest/fly-linux-amd64.tar.gz
    ```

2. Extract the tarball:
    ```bash
    tar -xzf fly-linux-amd64.tar.gz
    ```

3. Move the binary to a directory in your PATH:
    ```bash
    sudo mv fly-linux-amd64 /usr/local/bin/fly
    ```

4. Verify the installation:
    ```bash
    fly --version
    ```

## Usage

### Base Docker Compose

FlyWP has a base Docker Compose configuration for running MySQL, Redis, Ofelia, and Nginx Proxy that are shared for all sites hosted on the server. The base Docker Compose must be started before a site can be created.

```bash
fly base start              # starts the base services (mysql, redis, nginx-proxy)
fly base stop               # stops the base services   
fly base restart            # restarts the base services
```

### Site Operations

From anywhere inside a site folder, you can run the following commands that will be executed on a per-site basis.

```bash
fly start                   # starts the website (via Docker Compose)
fly stop                    # stops the website (via Docker Compose)
fly restart                 # restarts the website (via Docker Compose)
fly wp                      # execute WP-CLI commands
fly logs <container>        # view logs from all containers or a single one
fly restart <container>     # restart a container
fly exec <container>        # execute commands inside a container. Default: "php"
```

### WP-CLI

**wp-cli**: To access `wp-cli`, use the following command from anywhere in the website folder. The CLI will find the appropriate WordPress folder to execute the `wp` command.

```bash
fly wp
```

### Global Commands

A few helper commands to debug the server installation and start/stop all sites.

```bash
fly status                  # shows the status of the system
fly sites start             # starts all sites
fly sites stop              # stops all sites
fly sites restart           # stops and starts all sites
```

## License

This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.
