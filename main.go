// Package main provides the entry point for the Linear plugin.
package main

import "github.com/relicta-tech/relicta-plugin-sdk/plugin"

func main() {
	plugin.Serve(&LinearPlugin{})
}
