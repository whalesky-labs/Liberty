import { readFile } from 'node:fs/promises';
import { buildDistribution, checkDistributionHost, distributionPlan, parseDistributionArgs, prepareDistribution, productionPromotion, renderWix, verifyDistribution, acceptWindowsInstall } from './lib/native-distribution.mjs';

try {
  const options = parseDistributionArgs(process.argv.slice(2));
  if (options.command === 'check') {
    const template = await readFile(new URL('../packages/desktop-resources/distribution/windows.wxs.in', import.meta.url), 'utf8');
    for (const target of ['windows-x64', 'windows-x86']) renderWix(template, { target, version: '1.0.0', files: ['Liberty Native Candidate.exe', 'README.txt', 'LICENSE', 'THIRD_PARTY_NOTICES.txt', 'icon.ico', 'native-distribution-provenance.json'] });
    if (options.target) checkDistributionHost(options.target);
    console.log(JSON.stringify({ policyValid: true, targetHostChecked: Boolean(options.target), productionReady: false, productionPromotion }, null, 2));
  } else if (options.command === 'plan') console.log(JSON.stringify(distributionPlan(options.target), null, 2));
  else if (options.command === 'prepare') console.log(`Prepared non-distributable installer inputs: ${await prepareDistribution(options)}`);
  else if (options.command === 'build') console.log(JSON.stringify(await buildDistribution(options), null, 2));
  else if (options.command === 'verify') console.log(JSON.stringify(await verifyDistribution(options), null, 2));
  else console.log(JSON.stringify(await acceptWindowsInstall(options), null, 2));
} catch (error) { console.error(error.message); process.exitCode = 1; }
