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

### Site Operations

Website names should *preferably* be autocompleted.

```bash
fly site <website> up
fly site <website> down
fly site <website> restart
fly site <website> logs <container>
fly site <website> restart <container>
fly site <website> exec <container>
```

From anywhere inside a site folder:

```bash
fly up
fly down
fly restart
fly logs <container>
fly restart <container>
fly exec <container>
```

### Base Docker Compose

```bash
fly base stop
fly base start
fly base restart
```

### Global Commands

```bash
fly status
fly sites stop
fly sites start
fly sites restart
```
