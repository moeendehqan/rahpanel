import {defineConfig} from '@playwright/test';
export default defineConfig({testDir:'./e2e',workers:1,use:{baseURL:process.env.E2E_BASE_URL||'http://127.0.0.1:8080',viewport:{width:1440,height:1000},launchOptions:{executablePath:process.env.BROWSER_EXECUTABLE}},reporter:'list'});
