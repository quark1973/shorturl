// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.1

package handler

import (
	"io"
	"net/http"

	"shorturl/internal/logic"
	"shorturl/internal/svc"

	"github.com/zeromicro/go-zero/rest/httpx"
)

func FeishuEventHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		l := logic.NewFeishuEventLogic(r.Context(), svcCtx)
		resp, err := l.Handle(body)
		if err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		httpx.OkJsonCtx(r.Context(), w, resp)
	}
}
