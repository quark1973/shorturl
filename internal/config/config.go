// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.1

package config

import "github.com/zeromicro/go-zero/rest"

type Config struct {
	rest.RestConf

	ShortUrlDB struct {
		DSN string
	}

	Sequence struct {
		DSN    string
		BizTag string
		Step   uint64
	}

	ShortDomain string

	Redis struct {
		Addrs    []string
		Password string
		DB       int
		Cluster  bool
	}

	ShortUrlCacheTTL  int
	ShortUrlBloomBits uint64

	Feishu struct {
		AppID             string `json:",optional"`
		AppSecret         string `json:",optional"`
		VerificationToken string `json:",optional"`
		APIBase           string `json:",default=https://open.feishu.cn/open-apis"`
	}
}
