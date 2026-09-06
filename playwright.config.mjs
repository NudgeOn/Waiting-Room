// SPDX-License-Identifier: Apache-2.0
import {defineConfig} from '@playwright/test';
import path from 'node:path';
const root = process.cwd();
const env = {GOCACHE:path.join(root,'.cache/go-build'), GOMODCACHE:path.join(root,'.cache/go-mod')};
export default defineConfig({
  testDir:'test/browser', workers:1, retries:0, timeout:30000,
  reporter:'line', outputDir:'.cache/playwright-results',
  use:{browserName:'chromium', headless:true, viewport:{width:1448,height:1086}, trace:'off', screenshot:'off'},
  webServer:[
    {command:'go run ./cmd/wr-lab -hold -template calm -gateway-port 18082', url:'http://127.0.0.1:18082/livez', env, reuseExistingServer:false, gracefulShutdown:{signal:'SIGTERM',timeout:5000}},
    {command:'go run ./cmd/wr-lab -template calm -gateway-port 18084', url:'http://127.0.0.1:18084/livez', env, reuseExistingServer:false, gracefulShutdown:{signal:'SIGTERM',timeout:5000}},
  ],
});
