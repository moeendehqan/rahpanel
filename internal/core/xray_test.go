package core

import (
 "context"
 "encoding/json"
 "os"
 "os/exec"
 "path/filepath"
 "testing"
 "time"
 "github.com/rahpanel/rahpanel/internal/security"
)
func TestGeneratedConfigurationAcceptedByPinnedXray(t *testing.T){binary:=os.Getenv("TEST_XRAY_BIN");if binary==""{t.Skip("set TEST_XRAY_BIN to the pinned Xray executable")};dir:=t.TempDir();ca,e:=security.NewAuthority();if e!=nil{t.Fatal(e)};crt,key,_,e:=ca.Issue("node",true,[]string{"node.example.com"});if e!=nil{t.Fatal(e)};certPath:=filepath.Join(dir,"tls.crt");keyPath:=filepath.Join(dir,"tls.key");if e=os.WriteFile(certPath,crt,0600);e!=nil{t.Fatal(e)};if e=os.WriteFile(keyPath,key,0600);e!=nil{t.Fatal(e)};n:=Node{ID:ID(),Host:"203.0.113.1",Domain:"node.example.com",RealityTarget:"www.microsoft.com:443",RealitySNI:"www.microsoft.com"};if e=NewNodeKeys(&n);e!=nil{t.Fatal(e)};u:=User{ID:ID(),Enabled:true,ExpiresAt:time.Now().Add(time.Hour)};raw,e:=BuildConfig(n,[]User{u},[]Credential{{UserID:u.ID,NodeID:n.ID,UUID:ID(),Password:RandomToken()}},time.Now());if e!=nil{t.Fatal(e)};var cfg map[string]any;if e=json.Unmarshal(raw,&cfg);e!=nil{t.Fatal(e)};for _,in:=range cfg["inbounds"].([]any){m:=in.(map[string]any);stream,ok:=m["streamSettings"].(map[string]any);if !ok{continue};if tls,ok:=stream["tlsSettings"].(map[string]any);ok{tls["certificates"]=[]any{map[string]string{"certificateFile":certPath,"keyFile":keyPath}}}};raw,e=json.Marshal(cfg);if e!=nil{t.Fatal(e)};path:=filepath.Join(dir,"xray.json");if e=os.WriteFile(path,raw,0600);e!=nil{t.Fatal(e)};ctx,cancel:=context.WithTimeout(context.Background(),20*time.Second);defer cancel();out,e:=exec.CommandContext(ctx,binary,"run","-test","-config",path).CombinedOutput();if e!=nil{t.Fatalf("pinned Xray rejected generated config: %s",out)}}
