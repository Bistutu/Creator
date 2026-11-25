import { createReadStream } from 'node:fs';
import { stat } from 'node:fs/promises';
import { createServer } from 'node:http';
import { extname, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';

const dist = fileURLToPath(new URL('dist', import.meta.url));
const mime = {
  '.html': 'text/html; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.png': 'image/png',
  '.dmg': 'application/x-apple-diskimage',
  '.json': 'application/json',
  '.txt': 'text/plain; charset=utf-8',
};

createServer(async (request, response) => {
  try {
    const pathname = decodeURIComponent(new URL(request.url, 'http://127.0.0.1').pathname);
    const file = resolve(dist, `.${pathname === '/' ? '/index.html' : pathname}`);
    if (file.startsWith(dist + sep) && ['GET', 'HEAD'].includes(request.method)) {
      const info = await stat(file);
      if (info.isFile()) {
        response.writeHead(200, {
          'Content-Type': mime[extname(file)] ?? 'application/octet-stream',
          'Content-Length': info.size,
        });
        if (request.method === 'HEAD') response.end();
        else createReadStream(file).on('error', () => response.destroy()).pipe(response);
        return;
      }
    }
  } catch (error) {
    if (!['ENOENT', 'ENOTDIR', 'ERR_INVALID_URL'].includes(error.code) && !(error instanceof URIError)) {
      console.error(error.message);
    }
  }
  response.writeHead(404).end('Not found');
}).listen(41737, '127.0.0.1', () => console.log('http://127.0.0.1:41737'));
