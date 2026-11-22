package store

import (
	"testing"

	"lactssl/internal/model"
)

func TestZotrusOrderLifecycle(t *testing.T) {
	st := newTestStore(t)
	order := &model.ZotrusOrder{
		TaskID:      7,
		Domains:     []string{"example.com", "www.example.com"},
		KeyType:     "ecdsa",
		OrderURL:    "https://acme.example/order/1",
		AccountURI:  "https://acme.example/acct/1",
		FinalizeURL: "https://acme.example/order/1/finalize",
		KeyPEM:      "-----BEGIN EC PRIVATE KEY-----\nsecret\n-----END EC PRIVATE KEY-----\n",
		CSRDER:      "Y3Ny",
		Challenges: []model.ZotrusChallenge{{
			Domain:      "example.com",
			RecordName:  "_path.example.com",
			RecordType:  "TXT",
			RecordValue: "token",
			Status:      "pending",
		}},
	}
	if err := st.CreateZotrusOrder(order); err != nil {
		t.Fatal(err)
	}
	if order.ID == 0 {
		t.Fatal("订单 ID 未回填")
	}

	got, err := st.GetZotrusOrderByTask(7)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != order.ID {
		t.Fatalf("未读回订单: %+v", got)
	}
	if got.KeyPEM != order.KeyPEM {
		t.Fatal("私钥未正确解密")
	}
	if got.AccountURI != order.AccountURI || len(got.Challenges) != 1 || got.Challenges[0].RecordName != "_path.example.com" {
		t.Fatalf("订单字段不一致: %+v", got)
	}

	got.Challenges[0].Status = "valid"
	if err := st.UpdateZotrusOrderChallenges(got.ID, got.Challenges, model.ZotrusOrderIssued, ""); err != nil {
		t.Fatal(err)
	}
	updated, err := st.GetZotrusOrderByTask(7)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != model.ZotrusOrderIssued || updated.Challenges[0].Status != "valid" {
		t.Fatalf("状态未更新: %+v", updated)
	}

	if err := st.DeleteZotrusOrderByTask(7); err != nil {
		t.Fatal(err)
	}
	missing, err := st.GetZotrusOrderByTask(7)
	if err != nil {
		t.Fatal(err)
	}
	if missing != nil {
		t.Fatal("订单未删除")
	}
}
