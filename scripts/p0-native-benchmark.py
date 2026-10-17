#!/usr/bin/env python3
"""Generate a disposable 100k-commit repository and measure the real native app.
Run from the repository root. Pass --soak-minutes 60 for the one-hour scenario.
No user repositories, credentials or settings are modified.
"""
import argparse, json, os, pathlib, subprocess, tempfile, sys
p=argparse.ArgumentParser();p.add_argument('--soak-minutes',type=int,default=0);p.add_argument('--output',default='/tmp/gitclient-p0-native.json');a=p.parse_args()
if sys.platform!='darwin':raise SystemExit('此脚本使用 macOS 的 vmmap；请在 Mac 上运行。')
root=pathlib.Path(__file__).resolve().parent.parent
with tempfile.TemporaryDirectory(prefix='gitclient-p0-') as temp:
    repo=pathlib.Path(temp)/'repo';repo.mkdir()
    subprocess.run(['git','init','-q','-b','main',str(repo)],check=True)
    for k,v in [('user.name','P0 benchmark'),('user.email','bench@example.invalid'),('commit.gpgsign','false'),('core.hooksPath',str(pathlib.Path(temp)/'no-hooks'))]:subprocess.run(['git','-C',str(repo),'config',k,v],check=True)
    proc=subprocess.Popen(['git','-C',str(repo),'fast-import','--quiet'],stdin=subprocess.PIPE)
    stream=proc.stdin
    for i in range(100000):
        body=f'commit {i}\n'.encode();stream.write(f'commit refs/heads/main\ncommitter Benchmark <bench@example.invalid> {1700000000+i} +0000\ndata {len(body)}\n'.encode()+body)
        if i==0 or i==99999:
            text=''.join(f'line {n}: {"updated" if i else "initial"}\n' for n in range(10000)).encode()
            stream.write(f'M 100644 inline large.txt\ndata {len(text)}\n'.encode()+text)
        stream.write(b'\n')
    stream.close()
    if proc.wait():raise SystemExit('fast-import failed')
    subprocess.run(['git','-C',str(repo),'reset','--hard','-q'],check=True)
    binary=pathlib.Path(temp)/'gitclient-bench'
    subprocess.run(['go','build','-trimpath','-tags','p0bench,mygo_noinspector','-ldflags','-s -w -X github.com/egoist/mygo.production=1','-o',str(binary),'.'],cwd=root/'prototype',check=True)
    env=dict(os.environ,P0_SOAK_MINUTES=str(a.soak_minutes))
    app=subprocess.Popen([str(binary),'--p0-bench',str(repo),a.output],env=env)
    awake=None
    if sys.platform=='darwin':awake=subprocess.Popen(['/usr/bin/caffeinate','-d','-i','-u','-w',str(app.pid)])
    try:
        if app.wait():raise SystemExit('native app failed')
    finally:
        if awake:awake.terminate();awake.wait()
    print(pathlib.Path(a.output).read_text())
