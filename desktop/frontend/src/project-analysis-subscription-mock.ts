import type {Recipient} from './project-analysis-types';

export function subscriptionRecipientMock(scenario:string,original:Recipient):Recipient {
    if(!scenario.includes('subscription'))return original;
    return {providerName:'GitHub Copilot',modelName:'项目整理模型',modelID:'copilot/selected-model',protocol:'subscription',baseURL:'https://subscription.workshelf.invalid/copilot',subscription:{schema:1,proofID:'a'.repeat(64),accountID:'b'.repeat(64),agent:'copilot',accountLabel:'工作账号 · te••••st',modelID:'copilot/selected-model',endpoint:'https://api.enterprise.githubcopilot.com/chat/completions',wireModel:'verified-wire-model'}};
}

export function checkSubscriptionMock(scenario:string,method:string):void {
    if(scenario.includes('subscription-expired')&&method.startsWith('Preview')||scenario.includes('subscription-changed')&&(method.startsWith('Confirm')||method.startsWith('Resume'))){
        throw new Error('订阅发送证明已失效，请到模型与账号重新测试连接，再重新预览。');
    }
}
