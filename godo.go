// Package godokiota provides convenience constructors for the Kiota-generated
// DigitalOcean API client living in the ./client package.
package godokiota

import (
	"github.com/harshmaru7/godo-kiota/client"
	"github.com/microsoft/kiota-abstractions-go/authentication"
	khttp "github.com/microsoft/kiota-http-go"
)

// NewClientWithToken builds a DigitalOcean API client authenticated with a
// personal access token, sent as `Authorization: Bearer <token>`.
func NewClientWithToken(token string) (*client.DigitalOceanClient, error) {
	authProvider, err := authentication.NewApiKeyAuthenticationProvider(
		"Bearer "+token,
		"Authorization",
		authentication.HEADER_KEYLOCATION,
	)
	if err != nil {
		return nil, err
	}
	adapter, err := khttp.NewNetHttpRequestAdapter(authProvider)
	if err != nil {
		return nil, err
	}
	return client.NewDigitalOceanClient(adapter), nil
}
