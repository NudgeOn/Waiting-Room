// SPDX-License-Identifier: Apache-2.0
import {defineConfig} from '@playwright/test';
export default defineConfig({testDir:'test/preview-browser',workers:1,retries:0,timeout:60000,reporter:'line',outputDir:'.cache/preview-browser-results',use:{browserName:'chromium',headless:true,viewport:{width:1440,height:1050},trace:'off',screenshot:'off',video:'off'},webServer:{command:'./build/wrctl preview',url:'http://127.0.0.1:18770/livez',reuseExistingServer:false,timeout:15000,gracefulShutdown:{signal:'SIGTERM',timeout:15000}}});
