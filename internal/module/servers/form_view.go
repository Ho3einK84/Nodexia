package servers

import "strings"

type ServerFormViewData struct {
	ID                  int64
	Name                string
	Host                string
	Port                string
	AuthMode            string
	Username            string
	Tags                string
	Note                string
	CredentialStrategy  string
	CredentialRef       string
	HasStoredCredential bool
	TrafficResetDay     string
	Errors              map[string]string
	Action              string
	DeleteAction        string
}

func visibleCredentialRef(strategy, reference string) string {
	if strategy == CredentialStrategyExternalRef {
		return reference
	}
	return ""
}

func hasStoredSecret(strategy, reference string) bool {
	return strings.TrimSpace(reference) != "" &&
		(strategy == CredentialStrategyStored || strategy == CredentialStrategyRuntime)
}

func NewFormViewData(input FormInput, errors ValidationErrors) ServerFormViewData {
	return ServerFormViewData{
		Name:               input.Name,
		Host:               input.Host,
		Port:               input.Port,
		AuthMode:           input.AuthMode,
		Username:           input.Username,
		Tags:               input.Tags,
		Note:               input.Note,
		CredentialStrategy: input.CredentialStrategy,
		CredentialRef:      visibleCredentialRef(input.CredentialStrategy, input.CredentialRef),
		TrafficResetDay:    input.TrafficResetDay,
		Errors:             errors,
		Action:             "/servers",
	}
}

func EditFormViewData(server Server, errors ValidationErrors) ServerFormViewData {
	input := FormInputFromServer(server)
	return ServerFormViewData{
		ID:                  server.ID,
		Name:                input.Name,
		Host:                input.Host,
		Port:                input.Port,
		AuthMode:            input.AuthMode,
		Username:            input.Username,
		Tags:                input.Tags,
		Note:                input.Note,
		CredentialStrategy:  input.CredentialStrategy,
		CredentialRef:       visibleCredentialRef(server.CredentialStrategy, input.CredentialRef),
		HasStoredCredential: hasStoredSecret(server.CredentialStrategy, server.CredentialRef),
		TrafficResetDay:     input.TrafficResetDay,
		Errors:              errors,
		Action:              "/servers/" + formatID(server.ID) + "/edit",
		DeleteAction:        "/servers/" + formatID(server.ID) + "/delete",
	}
}
