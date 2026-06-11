package registry

import (
	"github.com/Ho3einK84/Nodexia/internal/module"
	"github.com/Ho3einK84/Nodexia/internal/module/commands"
	"github.com/Ho3einK84/Nodexia/internal/module/files"
	"github.com/Ho3einK84/Nodexia/internal/module/monitoring"
	"github.com/Ho3einK84/Nodexia/internal/module/nodes"
	"github.com/Ho3einK84/Nodexia/internal/module/servers"
	"github.com/Ho3einK84/Nodexia/internal/module/system"
)

func DefaultModules() []module.Module {
	return []module.Module{
		servers.New(),
		monitoring.New(),
		commands.New(),
		files.New(),
		nodes.New(),
		system.New(),
	}
}

func RouteGroups(modules []module.Module) []string {
	groups := make([]string, 0, len(modules))
	for _, mod := range modules {
		groups = append(groups, mod.RouteGroup())
	}

	return groups
}
