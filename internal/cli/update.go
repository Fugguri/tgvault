package cli

import (
	"context"
	"fmt"
	"os"
	"runtime"

	"github.com/Fugguri/tgvault/internal/update"
)

// Update проверяет и ставит свежий релиз tgvault с GitHub.
// checkOnly — только показать, force — переустановить, даже если версия та же.
func Update(ctx context.Context, version string, checkOnly, force bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	rel, err := update.Fetch(ctx, update.DefaultRepo, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	cur := update.Normalize(version)
	newer := force || update.Newer(rel.Tag, version)
	if newer {
		fmt.Printf("доступна версия %s (у тебя %s)\n", rel.Tag, cur)
	} else {
		fmt.Printf("✓ уже последняя версия (%s)\n", cur)
	}
	if checkOnly {
		if newer {
			fmt.Printf("обновиться: tgvault update\n%s\n", rel.Page)
		}
		return nil
	}
	if newer {
		if err := update.Install(ctx, rel, exe); err != nil {
			return err
		}
		fmt.Printf("✓ обновлено: %s\n%s\n", exe, rel.Page)
	}
	// Скилл лежит вне проекта и обновляется только здесь. Синкаем и когда бинарь
	// уже свежий: инструкция могла отстать сама по себе. Сеть отвалилась — не
	// роняем обновление, но и не молчим.
	if err := SyncSkill(ctx, update.DefaultRepo, rel.Tag); err != nil {
		fmt.Fprintf(os.Stderr, "  ⚠ %v\n", err)
	}
	return nil
}
