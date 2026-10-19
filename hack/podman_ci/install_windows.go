package main

import "context"

const podmanSearchRoot = `C:\`

func platformInstall(ctx context.Context, deps Dependencies, cfg InstallConfig) error {
	return installPodmanMSI(ctx, deps, cfg, podmanSearchRoot)
}
