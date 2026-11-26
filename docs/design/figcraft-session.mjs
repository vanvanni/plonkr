// Drive the running Figcraft control port through its official MCP adapter.
import { spawn } from 'node:child_process';
import { readFile } from 'node:fs/promises';
const executable = process.env.FIGCRAFT_CLI || 'figcraft-cli';
const child = spawn(executable, ['mcp', '--attach', process.env.FIGCRAFT_PORT || '7979'], { stdio: ['pipe', 'pipe', 'inherit'] });
let sequence = 0, buffer = '';
const pending = new Map();
child.stdout.on('data', chunk => {
  buffer += chunk;
  while (buffer.includes('\n')) {
    const at = buffer.indexOf('\n'), line = buffer.slice(0, at); buffer = buffer.slice(at + 1);
    try { const reply = JSON.parse(line); if (pending.has(reply.id)) { const p = pending.get(reply.id); pending.delete(reply.id); reply.error ? p.reject(new Error(JSON.stringify(reply.error))) : p.resolve(reply.result); } } catch {}
  }
});
child.on('exit', code => { for (const p of pending.values()) p.reject(new Error(`Figcraft adapter exited ${code}`)); });
function rpc(method, params = {}) {
  const id = ++sequence;
  return new Promise((resolve, reject) => { pending.set(id, {resolve,reject}); child.stdin.write(JSON.stringify({jsonrpc:'2.0',id,method,params})+'\n'); });
}
try {
  await rpc('initialize', {protocolVersion:'2024-11-05',capabilities:{},clientInfo:{name:'plonkr-design',version:'1.0'}});
  child.stdin.write(JSON.stringify({jsonrpc:'2.0',method:'notifications/initialized'})+'\n');
  if (!process.argv[2]) console.log(JSON.stringify(await rpc('tools/list'),null,2));
  else {
    const calls = JSON.parse(await readFile(process.argv[2], 'utf8'));
    for (const call of calls) {
      const result = await rpc('tools/call', call);
      console.log(JSON.stringify(result));
      if (result.isError) throw new Error('Figcraft command failed');
    }
  }
} finally { child.stdin.end(); child.kill(); }
