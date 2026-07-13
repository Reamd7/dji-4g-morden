import { useEffect } from 'react';
import { Tabs } from '@radix-ui/themes';
import { DeviceService } from '../bindings/dji-modem-research/desktop/services';
import DevicePage from './pages/DevicePage';
import SMSPage from './pages/SMSPage';
import NetworkPage from './pages/NetworkPage';

function App() {
  useEffect(() => {
    // 全局 DOM 上报(调试通道)
    const t = setInterval(() => {
      DeviceService.ReportDOM(document.documentElement.outerHTML).catch(() => {});
    }, 2000);
    return () => clearInterval(t);
  }, []);

  return (
    <Tabs.Root defaultValue="device">
      <Tabs.List>
        <Tabs.Trigger value="device">设备</Tabs.Trigger>
        <Tabs.Trigger value="sms">短信</Tabs.Trigger>
        <Tabs.Trigger value="network">上网</Tabs.Trigger>
      </Tabs.List>
      <Tabs.Content value="device">
        <DevicePage />
      </Tabs.Content>
      <Tabs.Content value="sms">
        <SMSPage />
      </Tabs.Content>
      <Tabs.Content value="network">
        <NetworkPage />
      </Tabs.Content>
    </Tabs.Root>
  );
}

export default App;
