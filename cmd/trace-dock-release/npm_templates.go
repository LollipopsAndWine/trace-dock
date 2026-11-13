package main

// npm needs a small Node launcher to select a native Go executable. These files
// are generated release assets; no Node toolchain is needed to build the CLI.
const npmLauncher = `#!/usr/bin/env node
'use strict';
const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const {spawn} = require('node:child_process');
const meta = require('../package.json');
const platforms = require('../platforms.json');
const selected = platforms[process.platform + '-' + process.arch];
function fail(message) { console.error('trace-dock: ' + message); process.exit(1); }
if (!selected) fail('Unsupported platform: ' + process.platform + '/' + process.arch);
let packageJSON;
try { packageJSON = require.resolve(selected.package + '/package.json'); }
catch { fail('Missing ' + selected.package + '. Reinstall ' + meta.name + ' with optional dependencies enabled (do not use --omit=optional).'); }
const nativeMeta = JSON.parse(fs.readFileSync(packageJSON, 'utf8'));
if (nativeMeta.name !== selected.package || nativeMeta.version !== meta.version) fail('Native package version does not match ' + meta.version + '; reinstall ' + meta.name + '.');
const binary = path.join(path.dirname(packageJSON), 'bin', selected.binary);
try {
  const digest = crypto.createHash('sha256').update(fs.readFileSync(binary)).digest('hex');
  if (digest !== selected.sha256) fail('Native executable checksum mismatch; reinstall ' + meta.name + '.');
} catch { fail('Cannot read native executable; reinstall ' + meta.name + '.'); }
const child = spawn(binary, process.argv.slice(2), {stdio:'inherit', env:{...process.env, TRACE_DOCK_NPM_PACKAGE:meta.name}});
const handlers = new Map();
for (const signal of ['SIGINT','SIGTERM','SIGHUP']) {
  const handler = () => { if (!child.killed) child.kill(signal); };
  handlers.set(signal, handler); process.on(signal, handler);
}
child.on('error', () => fail('Cannot start native executable.'));
child.on('exit', (code,signal) => {
  for (const [name,handler] of handlers) process.removeListener(name,handler);
  if (signal) { try { process.kill(process.pid,signal); } catch { process.exit(1); } }
  else process.exit(code === null ? 1 : code);
});
`

const npmPublisher = `#!/usr/bin/env node
'use strict';
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const crypto = require('node:crypto');
const {spawnSync} = require('node:child_process');
const plan = require('./publish.json');
const args = process.argv.slice(2);
if (args.length > 1 || (args.length === 1 && args[0] !== '--dry-run')) { console.error('Usage: node publish.cjs [--dry-run]'); process.exit(2); }
const dryRun = args[0] === '--dry-run';
let temp;
try {
  // Validate the complete publication set before performing any registry writes.
  for (const pkg of plan.packages) {
    if (path.basename(pkg.file) !== pkg.file || !pkg.file.endsWith('.tgz')) throw new Error('Unsafe tarball path.');
    const actual = 'sha512-' + crypto.createHash('sha512').update(fs.readFileSync(path.join(__dirname,pkg.file))).digest('base64');
    if (actual !== pkg.integrity) throw new Error('Changed tarball: ' + pkg.file);
  }
  if (dryRun) {
    console.log(JSON.stringify({registry:plan.registry,tag:plan.tag,packages:plan.packages.map(p=>p.name+'@'+p.version)},null,2));
  } else {
    const env = {...process.env};
    if (env.NPM_TOKEN) {
      const registry = new URL(plan.registry);
      temp = fs.mkdtempSync(path.join(os.tmpdir(),'trace-dock-npm-auth-'));
      const config = path.join(temp,'.npmrc');
      const authPath = registry.pathname.replace(/\/?$/,'/');
      fs.writeFileSync(config,'//'+registry.host+authPath+':_authToken=${NPM_TOKEN}\n',{mode:0o600});
      env.NPM_CONFIG_USERCONFIG=config;
    }
    const npm = process.platform==='win32' ? 'npm.cmd' : 'npm';
    if (process.platform==='win32') throw new Error('Run the publishing step on Linux/macOS; all supported platforms can install the resulting packages.');
    function view(pkg) {
      const r=spawnSync(npm,['view',pkg.name+'@'+pkg.version,'dist.integrity','--json','--registry',plan.registry],{env,encoding:'utf8'});
      if (r.error) throw new Error('Cannot run npm. Install Node.js/npm in the publishing environment.');
      let parsed;try{parsed=JSON.parse(r.stdout);}catch{}
      if(r.status===0 && typeof parsed==='string') return parsed;
      if(parsed && parsed.error && parsed.error.code==='E404') return null;
      if(r.status!==0 && /E404|404 Not Found|not_found/i.test((r.stdout||'')+' '+(r.stderr||''))) return null;
      throw new Error('Cannot check registry version for '+pkg.name+' (verify authentication, registry and connectivity).');
    }
    for (const pkg of plan.packages) {
      const existing=view(pkg);
      if(existing!==null){if(existing!==pkg.integrity)throw new Error('Registry already contains different bytes for '+pkg.name+'@'+pkg.version);console.log('Already published: '+pkg.name+'@'+pkg.version);continue;}
      const r=spawnSync(npm,['publish',path.join(__dirname,pkg.file),'--ignore-scripts','--access',plan.access,'--tag',plan.tag,'--registry',plan.registry],{env,stdio:'inherit'});
      if(r.error || r.status!==0){if(view(pkg)!==pkg.integrity)throw new Error('npm publish failed: '+pkg.name);}
    }
  }
} catch(error) { console.error('trace-dock npm publish: '+error.message);process.exitCode=1; }
finally { if(temp) fs.rmSync(temp,{recursive:true,force:true}); }
`
