import subprocess, os, time, json
from pathlib import Path
env = dict(os.environ, CGO_ENABLED='1', GOPROXY='https://goproxy.cn,direct')
checks = [
 ('user-verification', ['go','test','./appserver','-run','UserVerification','-count=1'], {}),
 ('appserver-doctor', ['go','test','./appserver','./doctor','-run','UserVerification|Desktop|Platform','-count=1'], {}),
 ('native-selection-readiness', ['go','test','./appserver','-run','TestDarwinUserVerificationNative(ProviderSelection|BiometricReadiness)$','-count=1','-v'], {}),
 ('sqlite', ['go','test','./state','-run','TestNativeSQLiteArtifactPlatform','-count=1','-v'], {'CODEX_EXPECT_GOOS':'darwin','CODEX_EXPECT_GOARCH':'amd64'}),
 ('build', ['go','build','-o','/tmp/darwin-align-codex','./cmd/codex'], {'GOOS':'darwin','GOARCH':'amd64'}),
 ('nocgo', ['go','test','./appserver','-run','UserVerificationUnsupportedPlatformProviderSelection','-count=1','-v'], {'CGO_ENABLED':'0','GOOS':'darwin','GOARCH':'amd64'}),
]
results=[]
for name, command, extra in checks:
    start=time.time()
    print('RUN', name, flush=True)
    with open('/tmp/darwin-align-'+name+'.log','w') as out:
        result=subprocess.run(command, env=env|extra, stdout=out, stderr=subprocess.STDOUT)
    results.append({'name':name,'command':command,'env':{'CGO_ENABLED':env['CGO_ENABLED'],'GOPROXY':env['GOPROXY']}|extra,'exit_code':result.returncode,'elapsed_seconds':round(time.time()-start,3)})
    Path('/tmp/darwin-align-check-results.json').write_text(json.dumps(results,ensure_ascii=False,indent=2)+'\n')
    print('DONE', name, result.returncode, flush=True)
