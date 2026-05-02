package orchid

import "sync"

type ToolName string

type Permission string

func PermissionFromTool(name ToolName) Permission {
	return Permission("tool:" + string(name))
}

var PermissionReadOnlyFS = Permission("fs:read-only")

type permissionBundleRegistry struct {
	mu      sync.RWMutex
	bundles map[Permission][]ToolName
}

func newPermissionBundleRegistry() *permissionBundleRegistry {
	return &permissionBundleRegistry{
		bundles: map[Permission][]ToolName{
			PermissionReadOnlyFS: {"read", "list", "search"},
		},
	}
}

func (r *permissionBundleRegistry) register(p Permission, tools ...ToolName) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bundles[p] = tools
}

func (r *permissionBundleRegistry) expandsTo(p Permission, tool ToolName) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tools, ok := r.bundles[p]
	if !ok {
		return false
	}
	for _, t := range tools {
		if t == tool {
			return true
		}
	}
	return false
}
