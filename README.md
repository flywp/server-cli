# server-cli

Easy CLI tool for servers managed by FlyWP.

## Installation

### Prerequisites

- [Docker](https://www.docker.com/get-started)
- [Docker Compose](https://docs.docker.com/compose/install/)

### Quick Install

You can easily install the `fly` CLI tool using the following command. This will download and run the `install.sh` script, which will automatically detect your operating system and architecture, download the latest release, and install it to `/usr/local/bin`:

```bash
curl -sL https://raw.githubusercontent.com/flywp/server-cli/main/install.sh | sudo bash
```

<details>

<summary>Manual Installation</summary>

### Manual Installation

If you prefer to manually download and install the binary, follow these steps:

1. Download the precompiled binaries from the [Releases](https://github.com/flywp/server-cli/releases) page. Choose the version suitable for your operating system and architecture.

1. Download the [latest tarball]((https://github.com/flywp/server-cli/releases)) for your platform:

    ```bash
    wget https://github.com/flywp/server-cli/releases/download/v0.1.0/fly-linux-amd64.tar.gz
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
    fly version
    ```

</details>

## Usage

### Base Docker Compose

FlyWP has a base Docker Compose configuration for running MySQL, Redis, Ofelia, and Nginx Proxy that are shared for all sites hosted on the server. The base Docker Compose must be started before a site can be created.

```bash
fly base start              # starts the base services (mysql, redis, nginx-proxy)
fly base stop               # stops the base services   
fly base restart            # restarts the base services
```

### Site Operations

You can run the following commands from anywhere inside a site folder or by specifying the domain name.

```bash
fly start --domain example.com       # starts the website
fly stop --domain example.com        # stops the website
fly restart --domain example.com     # restarts the website
fly wp --domain example.com          # execute WP-CLI commands
fly logs --domain example.com        # view logs from all containers or a single one
fly restart <container> --domain example.com  # restart a container
fly exec <container> --domain example.com     # execute commands inside a container. Default: "php"
```

Or run the commands from within the site directory without specifying the domain:

```bash
fly start                   # starts the website
fly stop                    # stops the website
fly restart                 # restarts the website
fly wp                      # execute WP-CLI commands
fly logs <container>        # view logs from all containers or a single one
fly restart <container>     # restart a container
fly exec <container>        # execute commands inside a container. Default: "php"
```

### WP-CLI

**wp-cli**: To access `wp-cli`, use the following command from anywhere in the website folder or specify the domain name. The CLI will find the appropriate WordPress folder to execute the `wp` command.

```bash
fly wp --domain example.com
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
