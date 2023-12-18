# server-cli
CLI tool for servers managed by FlyWP

## Commands

### WP-CLI

**wp-cli**: To access the `wp-cli`, you can use the following command from anywhere in the website folder, and the CLI will find the appropriate WordPress folder to execute the `wp` command.

```bash
fly wp
```

**Any site**:

```bash
fly site <website> wp
```

In-case you want to run a `wp` command in a specific webwebsite, you could run like this.

