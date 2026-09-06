// SPDX-License-Identifier: Apache-2.0
import {defineConfig} from '@playwright/test';
export default defineConfig({testDir:'test/admin-browser',workers:1,retries:0,timeout:90000,reporter:'line',outputDir:'.cache/admin-browser-results',use:{browserName:'chromium',headless:true,ignoreHTTPSErrors:true,viewport:{width:1586,height:992},trace:'off',screenshot:'off',video:'off'}});
