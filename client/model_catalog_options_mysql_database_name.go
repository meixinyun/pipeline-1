/*
 * Pipeline API
 *
 * Pipeline v0.3.0 swagger
 *
 * API version: 0.3.0
 * Contact: info@banzaicloud.com
 * Generated by: OpenAPI Generator (https://openapi-generator.tech)
 */

package client

type CatalogOptionsMysqlDatabaseName struct {
	Name string `json:"name,omitempty"`
	Type string `json:"type,omitempty"`
	Info string `json:"info,omitempty"`
	Default string `json:"default,omitempty"`
	Readonly bool `json:"readonly,omitempty"`
	Enabled bool `json:"enabled,omitempty"`
	Key string `json:"key,omitempty"`
}
