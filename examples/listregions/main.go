// Smoke test for the Kiota-generated DigitalOcean Go SDK: list all regions.
//
//	DIGITALOCEAN_TOKEN=dop_v1_... go run ./examples/listregions
package main

import (
	"context"
	"fmt"
	"os"

	godo "github.com/harshmaru7/godo-kiota"
)

func main() {
	token := os.Getenv("DIGITALOCEAN_TOKEN")
	if token == "" {
		fmt.Fprintln(os.Stderr, "set DIGITALOCEAN_TOKEN")
		os.Exit(1)
	}

	client, err := godo.NewClientWithToken(token)
	if err != nil {
		fmt.Fprintln(os.Stderr, "client:", err)
		os.Exit(1)
	}

	resp, err := client.V2().Regions().Get(context.Background(), nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "list regions:", err)
		os.Exit(1)
	}

	regions := resp.GetRegions()
	fmt.Printf("found %d regions:\n", len(regions))
	for _, r := range regions {
		slug, name := "", ""
		if r.GetSlug() != nil {
			slug = *r.GetSlug()
		}
		if r.GetName() != nil {
			name = *r.GetName()
		}
		available := r.GetAvailable() != nil && *r.GetAvailable()
		fmt.Printf("  %-5s %-22s available=%v\n", slug, name, available)
	}
}
