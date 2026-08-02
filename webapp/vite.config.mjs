// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Plain .mjs rather than .ts on purpose: Vite 7's own types need TypeScript >= 5
// and this project is pinned to TypeScript 4.6.

import {createReadStream, existsSync} from 'fs';
import {cp} from 'fs/promises';
import path from 'path';
import {fileURLToPath} from 'url';

import formatjs from '@formatjs/unplugin/vite';
import react from '@vitejs/plugin-react';
import {defineConfig} from 'vite';

const root = path.dirname(fileURLToPath(import.meta.url));
const staticDir = path.join(root, 'static');
const outDir = path.join(root, 'pack');

// React Compiler roughly doubles a build (7s -> 15s here), which is felt most in
// `wails dev`, where it sits between saving a file and the window reloading.
// NO_REACT_COMPILER=1 turns it off for a faster loop, or to tell a compiler bug
// apart from one of ours. Release builds always have it on.
const compilerEnabled = !process.env.NO_REACT_COMPILER;

// Where `npm run dev` proxies the API: the standalone server's default port
// from config.json, i.e. whatever `make watch` is running in another terminal.
const devServerURL = 'http://localhost:8000';

const mimeTypes = {
    '.gif': 'image/gif',
    '.jpg': 'image/jpeg',
    '.jpeg': 'image/jpeg',
    '.png': 'image/png',
    '.svg': 'image/svg+xml',
};

// In a build the Go server resolves {{.BaseURL}} when it templates index.html;
// the dev server serves the file as-is, so the placeholder is dropped here.
function focalboardHtml() {
    return {
        name: 'focalboard-html',
        apply: 'serve',
        transformIndexHtml: {
            order: 'pre',
            handler(html) {
                return html.replace(/\{\{\.BaseURL\}\}/g, '');
            },
        },
    };
}

// `static/` is served from `/static` in dev and copied to `pack/static` on build
// — the same contract the Go server expects (it mounts pack/static at /static).
// Assets imported from TypeScript are emitted into pack/static by Vite itself.
function focalboardStatic() {
    return {
        name: 'focalboard-static',
        configureServer(server) {
            server.middlewares.use('/static', (req, res, next) => {
                const rel = decodeURIComponent((req.url || '').split('?')[0]);
                const file = path.join(staticDir, rel);
                if (!file.startsWith(staticDir) || !existsSync(file)) {
                    next();
                    return;
                }
                const type = mimeTypes[path.extname(file).toLowerCase()];
                if (type) {
                    res.setHeader('Content-Type', type);
                }
                createReadStream(file).pipe(res);
            });
        },
        async closeBundle() {
            await cp(staticDir, path.join(outDir, 'static'), {recursive: true});
        },
    };
}

export default defineConfig(({mode}) => {
    const isProduction = mode === 'production';

    return {
        root,
        base: '/',

        // Handled by focalboardStatic: Vite would copy publicDir to the output
        // root, but these files have to land in pack/static.
        publicDir: false,

        plugins: [
            // formatjs's own universal build plugin, standing in for the
            // @formatjs/ts-transformer that ran under ts-loader: pre-compile
            // messages to an ICU AST, and hash-generate an id only where the
            // source doesn't declare one. Every message in src/ declares an
            // explicit id, which is what keeps them matching i18n/*.json.
            formatjs({
                ast: true,
                idInterpolationPattern: '[sha512:contenthash:base64:6]',
            }),
            react({
                // tsconfig still asks for the classic `jsx: "react"` transform, and
                // every source file imports React itself.
                jsxRuntime: 'classic',

                babel: {
                    plugins: compilerEnabled ? [
                        // React Compiler, inferring which components and hooks it
                        // can memoise on its own. `panicThreshold: 'none'` makes it
                        // skip anything it cannot prove instead of failing the
                        // build, so a component that breaks the Rules of React
                        // simply stays as written -- see the react-hooks warnings
                        // in eslint.config.mjs for what those are.
                        ['babel-plugin-react-compiler', {
                            target: '19',
                            compilationMode: 'infer',
                            panicThreshold: 'none',
                            logger: process.env.REACT_COMPILER_LOG ? {
                                logEvent(filename, event) {
                                    // eslint-disable-next-line no-console
                                    console.log(JSON.stringify({filename, kind: event.kind, fnName: event.fnLoc && event.fnName, detail: event.detail && (event.detail.reason || event.detail.description)}))
                                },
                            } : null,
                        }],
                    ] : [],
                },
            }),
            focalboardHtml(),
            focalboardStatic(),
        ],

        build: {
            outDir,
            assetsDir: 'static',
            emptyOutDir: true,
            target: 'es2019',
            sourcemap: !isProduction,
            minify: isProduction,
        },

        experimental: {
            // The server rewrites {{.BaseURL}} in index.html, so HTML references
            // carry the placeholder while asset references inside JS/CSS stay
            // relative to the emitting chunk (webpack's `publicPath: 'auto'`).
            renderBuiltUrl(filename, {hostType}) {
                if (hostType === 'html') {
                    return `{{.BaseURL}}/${filename}`;
                }
                return {relative: true};
            },
        },

        css: {
            preprocessorOptions: {
                scss: {
                    api: 'modern-compiler',
                    silenceDeprecations: ['import', 'global-builtin'],
                },
            },
        },

        // `npm run dev` — browser-only dev server with HMR, run alongside
        // `make watch`. The desktop app does not use it: `make dev-wails` keeps
        // serving the page from the Go server and rebuilds pack/ on the side.
        server: {
            port: 5173,
            strictPort: true,
            proxy: Object.fromEntries(
                ['/api', '/plugins', '/files', '/login', '/register', '/logout'].
                    map((route) => [route, {target: devServerURL, changeOrigin: true}]),
            ),
        },
    };
});
