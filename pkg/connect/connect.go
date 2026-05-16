package connect

import (
	"net/http"
	"time"

	"github.com/zeromicro/go-zero/core/logx"
)

//全局客户端HTTP client
var client = &http.Client{
	Transport: &http.Transport{
		DisableKeepAlives: true,
	},
	Timeout: 2*time.Second,
}

// Get 判断url是否请求通
func Get(url string)bool{
	resp,err:=client.Get(url)
	if err!=nil{
		logx.Errorw("connected client.Get Failed",logx.LogField{
			Key: "err",
			Value: err.Error(),
		})
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK //别人给我发一个跳转也不算过
}
