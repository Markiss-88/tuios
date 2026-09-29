#!/usr/bin/env bash
# Capture raw pio control-socket replies for client tests.
# Default host: SOCK=$SOCK ./capture.sh captures all normal replies, including recent jobs.
# Live host: SEED_LIVE_JOB=1 ... then SOCK=$SOCK LIVE_ONLY=1 ./capture.sh captures
# jobs.list.live and harness.set.busy without replacing jobs.list.recent's ended job #3.
set -euo pipefail

: "${SOCK:?set SOCK to pio runtime/control.sock}"
dir="$(cd "$(dirname "$0")" && pwd)/wire"
mkdir -p "$dir"

request() {
	local name=$1 req=$2
	SOCK="$SOCK" REQ="$req" node -e '
const n=require("node:net"),s=n.createConnection(process.env.SOCK);let b=Buffer.alloc(0);
s.on("connect",()=>s.write(process.env.REQ+"\n"));
s.on("data",c=>{b=Buffer.concat([b,c]);for(;;){const i=b.indexOf(10);if(i<0)break;const l=b.subarray(0,i);b=b.subarray(i+1);console.log(l.toString());if(!("event" in JSON.parse(l)))process.exit(0)}});
s.on("error",e=>{console.error(e);process.exit(1)})' >"$dir/$name.jsonl"
}

if [[ ${LIVE_ONLY:-} == 1 ]]; then
	request jobs.list.live '{"id":1,"verb":"jobs.list"}'
	request harness.set.busy '{"id":1,"verb":"harness.set","params":{"name":"codex"}}'
	exit 0
fi

request ping '{"id":1,"verb":"ping"}'
request sessions.list '{"id":1,"verb":"sessions.list"}'
request sessions.history '{"id":1,"verb":"sessions.history","params":{"id":"default","limit":50}}'
request sessions.create '{"id":1,"verb":"sessions.create","params":{"name":"Captured"}}'
request config.get '{"id":1,"verb":"config.get"}'
request tasks.list '{"id":1,"verb":"tasks.list"}'
request tasks.list.working '{"id":1,"verb":"tasks.list","params":{"status":"working"}}'
request goals.list '{"id":1,"verb":"goals.list"}'
request jobs.list.recent '{"id":1,"verb":"jobs.list","params":{"recent":true}}'
request tasks.create '{"id":1,"verb":"tasks.create","params":{"title":"Made from socket"}}'
request goals.create '{"id":1,"verb":"goals.create","params":{"title":"Goal from socket"}}'
request harness.set.ok '{"id":1,"verb":"harness.set","params":{"name":"claude-code"}}'
request harness.set.internal '{"id":1,"verb":"harness.set","params":{"name":"agent-zero"}}'
request chat.send '{"id":1,"verb":"chat.send","params":{"session":"default","text":"hello"}}'
request chat.send.bad-session '{"id":1,"verb":"chat.send","params":{"session":"nope","text":"hello"}}'
request bad-verb '{"id":1,"verb":"bad.verb"}'
