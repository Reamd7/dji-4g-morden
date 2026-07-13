import { useState, useEffect, useCallback } from 'react';
import { Card, Button, Text, Flex, Badge, Heading, Callout } from '@radix-ui/themes';
import { DeviceService, type USBDeviceInfo } from '../bindings/dji-modem-research/desktop/services';

function App() {
  const [devices, setDevices] = useState<USBDeviceInfo[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const list = await DeviceService.ListDevices();
      setDevices(list ?? []);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setDevices([]);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  return (
    <Flex direction="column" gap="4" p="6">
      <Flex justify="between" align="center">
        <Heading size="6">设备发现</Heading>
        <Button onClick={refresh} variant="soft" disabled={loading}>
          {loading ? '扫描中…' : '刷新'}
        </Button>
      </Flex>

      {error && (
        <Callout.Root color="red" size="1">
          <Callout.Text>扫描失败:{error}</Callout.Text>
        </Callout.Root>
      )}

      {!loading && !error && devices.length === 0 && (
        <Card>
          <Flex align="center" justify="center" p="5">
            <Text color="gray" size="2" align="center">
              未发现 DJI 百望 4G 模组(VID 2C7C)。请确认设备已通过 USB 连接并刷成标准 EC25 PID。
            </Text>
          </Flex>
        </Card>
      )}

      {devices.map((d, i) => (
        <Card key={`${d.vid}:${d.pid}:${i}`} size="3">
          <Flex direction="column" gap="3">
            <Flex justify="between" align="center">
              <Heading size="4">{d.product || 'DJI 百望 4G 模组'}</Heading>
              <Badge color="green" variant="soft" size="2">已发现</Badge>
            </Flex>
            <Flex direction="column" gap="1">
              <Row label="VID:PID" value={`${d.vid}:${d.pid}`} />
              <Row label="厂商" value={d.vendor || '—'} />
              <Row label="序列号" value={d.serial || '—'} />
              <Row label="接口数" value={`${d.interfaces}`} />
            </Flex>
          </Flex>
        </Card>
      ))}
    </Flex>
  );
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <Flex justify="between">
      <Text as="span" size="2" color="gray">{label}</Text>
      <Text as="span" size="2" color="gray" highContrast>{value}</Text>
    </Flex>
  );
}

export default App;
