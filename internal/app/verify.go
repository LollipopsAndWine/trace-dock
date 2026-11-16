package app

import (
	"context"
	"fmt"
	"time"

	"github.com/trace-dock/trace-dock/internal/api"
	"github.com/trace-dock/trace-dock/internal/ui"
)

func completeWithProgress(ctx context.Context, u *ui.UI, client *api.Client, id string, completion api.Completion) (map[string]any, error) {
	u.VerifyUpload()
	progressCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(120 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-progressCtx.Done():
				return
			case <-ticker.C:
				u.RefreshVerification()
			}
		}
	}()
	var receipt map[string]any
	err := client.Call(ctx, "POST", "/v1/collections/"+id+"/complete", completion, &receipt, "")
	stop()
	<-done
	if err == nil && receipt["state"] != "completed" {
		err = fmt.Errorf("服务端尚未完成对象校验，可再次运行恢复")
	}
	if err != nil {
		u.InterruptUpload()
		return nil, err
	}
	return receipt, nil
}
