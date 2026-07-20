import { readFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const specification = resolve(root, '..', 'specs', '001-isureal-social-app', 'contracts', 'openapi.yaml');
const generated = resolve(root, 'src', 'api', 'schema.d.ts');
const cli = resolve(root, 'node_modules', 'openapi-typescript', 'bin', 'cli.js');
const result = spawnSync(process.execPath, [cli, specification], { encoding: 'utf8', cwd: root });
if (result.status !== 0) {
  process.stderr.write(result.stderr);
  process.exit(result.status ?? 1);
}
const normalize = (value) => value.replaceAll('\r\n', '\n').trimEnd();
if (normalize(result.stdout) !== normalize(readFileSync(generated, 'utf8'))) {
  console.error('OpenAPI generated client is stale. Run: npm run generate:api');
  process.exit(1);
}
console.log('OpenAPI generated client is current.');
