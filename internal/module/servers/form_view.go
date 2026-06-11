package servers

type ServerFormViewData struct {
	ID                 int64
	Name               string
	Host               string
	Port               string
	AuthMode           string
	Username           string
	Tags               string
	Note               string
	CredentialStrategy string
	CredentialRef      string
	Errors             map[string]string
	Action             string
	DeleteAction       string
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
		CredentialRef:      input.CredentialRef,
		Errors:             errors,
		Action:             "/servers",
	}
}

func EditFormViewData(server Server, errors ValidationErrors) ServerFormViewData {
	input := FormInputFromServer(server)
	return ServerFormViewData{
		ID:                 server.ID,
		Name:               input.Name,
		Host:               input.Host,
		Port:               input.Port,
		AuthMode:           input.AuthMode,
		Username:           input.Username,
		Tags:               input.Tags,
		Note:               input.Note,
		CredentialStrategy: input.CredentialStrategy,
		CredentialRef:      input.CredentialRef,
		Errors:             errors,
		Action:             "/servers/" + formatID(server.ID) + "/edit",
		DeleteAction:       "/servers/" + formatID(server.ID) + "/delete",
	}
}
