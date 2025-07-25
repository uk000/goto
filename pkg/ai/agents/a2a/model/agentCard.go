/**
 * Copyright 2025 uk
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package a2amodel

type AnyMap map[string]any
type Schemes map[string][]string

type OAuthFlow struct {
	AuthorizationUrl string            `json:"authorizationUrl"`
	TokenUrl         string            `json:"tokenUrl"`
	RefreshUrl       string            `json:"refreshUrl"`
	Scopes           map[string]string `json:"scopes"`
}

type OAuthFlows struct {
	Implicit          *OAuthFlow `json:"implicit"`
	Password          *OAuthFlow `json:"password"`
	ClientCredentials *OAuthFlow `json:"clientCredentials"`
	AuthorizationCode *OAuthFlow `json:"authorizationCode"`
}

type SecurityScheme struct {
	AnyMap
	Type             string      `json:"type"`
	Name             string      `json:"name"`
	Description      string      `json:"description,omitempty"`
	In               string      `json:"in"`
	Scheme           string      `json:"scheme"`
	BearerFormat     string      `json:"bearerFormat,omitempty"`
	Flows            *OAuthFlows `json:"flows"`
	OpenIdConnectUrl string      `json:"openIdConnectUrl"`
}

type AgentExtension struct {
	URI         string         `json:"uri"`
	Description string         `json:"description,omitempty"`
	Required    bool           `json:"required,omitempty"`
	Params      map[string]any `json:"params,omitempty"`
}

type AgentCapabilities struct {
	Streaming              bool              `json:"streaming,omitempty"`
	PushNotifications      bool              `json:"pushNotifications,omitempty"`
	StateTransitionHistory bool              `json:"stateTransitionHistory,omitempty"`
	Extensions             []*AgentExtension `json:"extensions,omitempty"`
}

type AgentProvider struct {
	Organization string `json:"organization"`
	URL          string `json:"url,omitempty"`
}

type AgentSkill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags,omitempty"`
	Examples    []string `json:"examples,omitempty"`
	InputModes  []string `json:"inputModes,omitempty"`
	OutputModes []string `json:"outputModes,omitempty"`
}

type AgentInterface struct {
	URL       string `json:"url"`
	Transport string `json:"transport"`
}

type AgentCard struct {
	ProtocolVersion      string                     `json:"protocolVersion"`
	Name                 string                     `json:"name"`
	Description          string                     `json:"description"`
	URL                  string                     `json:"url"`
	PreferredTransport   string                     `json:"preferredTransport,omitempty"`
	AdditionalInterfaces []*AgentInterface          `json:"additionalInterfaces,omitempty"`
	Provider             *AgentProvider             `json:"provider,omitempty"`
	IconUrl              string                     `json:"iconUrl,omitempty"`
	Version              string                     `json:"version"`
	DocumentationURL     string                     `json:"documentationUrl,omitempty"`
	Capabilities         *AgentCapabilities         `json:"capabilities"`
	SecuritySchemes      map[string]*SecurityScheme `json:"securitySchemes,omitempty"`
	Security             *Schemes                   `json:"security,omitempty"`
	DefaultInputModes    []string                   `json:"defaultInputModes"`
	DefaultOutputModes   []string                   `json:"defaultOutputModes"`
	Skills               []*AgentSkill              `json:"skills"`
	AuthExtCard          bool                       `json:"supportsAuthenticatedExtendedCard,omitempty"`
}
