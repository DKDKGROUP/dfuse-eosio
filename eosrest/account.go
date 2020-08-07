// Copyright 2020 dfuse Platform Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package eosrest

import (
	"context"

	"github.com/dfuse-io/derr"
	"github.com/dfuse-io/kvdb"
	eos "github.com/eoscanada/eos-go"
)

var AccountGetterInstance AccountGetter

type AccountGetter interface {
	GetAccount(ctx context.Context, name string) (out *eos.AccountResp, err error)
}

type APIAccountGetter struct {
	api *eos.API
}

func (g *APIAccountGetter) GetAccount(ctx context.Context, name string) (out *eos.AccountResp, err error) {
	out, err = g.api.GetAccount(ctx, eos.AccountName(name))
	if err == eos.ErrNotFound {
		return nil, DBAccountNotFoundError(ctx, name)
	}

	return
}

func NewApiAccountGetter(api *eos.API) *APIAccountGetter {
	return &APIAccountGetter{
		api: api,
	}
}

func isAccountNotFoundError(err error) bool {
	if err == kvdb.ErrNotFound {
		return true
	}

	return derr.ToErrorResponse(context.Background(), err).Code == "data_account_not_found_error"
}
