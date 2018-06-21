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

type HelmChartDetailsResponseChart struct {
	Name string `json:"name,omitempty"`
	Home string `json:"home,omitempty"`
	Sources []string `json:"sources,omitempty"`
	Version string `json:"version,omitempty"`
	Description string `json:"description,omitempty"`
	Keywords []string `json:"keywords,omitempty"`
	Maintainers []HelmChartDetailsResponseChartMaintainers `json:"maintainers,omitempty"`
	Engine string `json:"engine,omitempty"`
	Icon string `json:"icon,omitempty"`
	AppVersion string `json:"appVersion,omitempty"`
	ApiVersion string `json:"apiVersion,omitempty"`
	Deprecated bool `json:"deprecated,omitempty"`
	Urls []string `json:"urls,omitempty"`
	Created string `json:"created,omitempty"`
	Digest string `json:"digest,omitempty"`
}
