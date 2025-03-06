package utils

import "github.com/fatih/color"

func ShowNoComposeError() {
	color.Red("No docker-compose.yml file found!")

	color.Yellow("You are not inside a site directory.")
	color.Yellow("Please run this command from inside a site directory, e.g:")
	color.Yellow("  cd ~/example.com")
	color.Yellow("  fly start")
	color.Yellow("\nOr specify the domain name:")
	color.Yellow("  fly start --domain example.com")
}
