package hub

import (
 "context"
 "crypto/sha256"
 "encoding/hex"
 "encoding/json"
 "net/http/httptest"
 "os"
 "path/filepath"
 "strings"
 "testing"
)

func TestBootstrapDistributionAndAdmission(t *testing.T) {
 cfg:=Config{}
 cfg.Bootstrap.Enabled=true
 cfg.Bootstrap.AssetsDir=t.TempDir()
 cfg.MeshCentral.Enabled=true
 cfg.MeshCentral.PublicURL="https://meshcentral.example.com"
 cfg.MeshCentral.MeshID="mesh//EXAMPLE@GROUP$ID"
 cfg.MeshCentral.Password="private-management-password"
 for _,name:=range append([]string{"client/bootstrap.sh"},bootstrapCommon...) {
  target:=filepath.Join(cfg.Bootstrap.AssetsDir,name)
  if err:=os.MkdirAll(filepath.Dir(target),0700);err!=nil {t.Fatal(err)}
  if err:=os.WriteFile(target,[]byte("example asset "+name),0600);err!=nil {t.Fatal(err)}
 }
 store:=testStore(t)
 _,token,_:=store.NewToken("test","",0)
 credential:=randomHex(32)
 serial,err:=store.Register(context.Background(),token,randomHex(32),credential)
 if err!=nil {t.Fatal(err)}
 server:=NewServer(cfg,store);defer server.Close()
 call:=func(path,auth string)(int,string){
  request:=httptest.NewRequest("GET",path,nil)
  if auth!="" {request.Header.Set("Authorization","Bearer "+auth)}
  recorder:=httptest.NewRecorder();server.ServeHTTP(recorder,request)
  return recorder.Code,recorder.Body.String()
 }
 if code,_:=call("/install.sh","");code!=200 {t.Fatal(code)}
 code,body:=call("/bootstrap/manifest?arch=amd64","")
 if code!=200 {t.Fatal(code,body)}
 var manifest struct{Files map[string]struct{SHA256 string `json:"sha256"`};Terminal bool `json:"terminal_enabled"`}
 if err=json.Unmarshal([]byte(body),&manifest);err!=nil {t.Fatal(err)}
 hash:=sha256.Sum256([]byte("example asset client/nodehub-init.py"))
 if manifest.Files["client/nodehub-init.py"].SHA256!=hex.EncodeToString(hash[:]) {t.Fatal("incorrect asset hash")}
 for _,path:=range []string{"/bootstrap/files/config.json","/bootstrap/files/client/activation.json","/bootstrap/files/client/state.json"} {
  if code,_:=call(path,"");code!=404 {t.Fatalf("unexpected public asset %s: %d",path,code)}
 }
 if code,_:=call("/bootstrap/manifest?arch=unknown","");code!=400 {t.Fatal(code)}
 endpoint:="/api/node/"+serial+"/bootstrap?arch=amd64"
 if code,_:=call(endpoint,"");code!=401 {t.Fatal("group download exposed without node authentication",code)}
 code,body=call(endpoint,credential)
 if code!=200||!strings.Contains(body,"meshagents?id=6")||strings.Contains(body,cfg.MeshCentral.Password) {t.Fatal("invalid or leaked bootstrap metadata",code)}
 if !strings.Contains(body,"EXAMPLE%40GROUP%24ID") {t.Fatal("group URL is not encoded")}
 if code,_:=call("/api/node/"+serial+"/bootstrap?arch=arm64",credential);code!=400 {t.Fatal("unsupported MeshAgent architecture accepted")}
 // Symlinks must not turn the public allowlist into a secret-file reader.
 path:=filepath.Join(cfg.Bootstrap.AssetsDir,"client/nodehub-init.py")
 if err=os.Remove(path);err!=nil {t.Fatal(err)}
 if err=os.Symlink(filepath.Join(cfg.Bootstrap.AssetsDir,"private.json"),path);err!=nil {t.Fatal(err)}
 if server.bootstrapAvailable() {t.Fatal("incomplete or linked assets advertised")}
 cfg.Bootstrap.Enabled=false
 disabled:=NewServer(cfg,store);defer disabled.Close()
 request:=httptest.NewRequest("GET","/install.sh",nil);response:=httptest.NewRecorder();disabled.ServeHTTP(response,request)
 if response.Code!=503 {t.Fatal("disabled bootstrap served installer")}
}
