import { useState, useEffect, useCallback } from 'react';
import { Card, Button, Text, Flex, TextField, Heading, Badge, Callout } from '@radix-ui/themes';
import { DialerService, type ConnectionInfo, type RelayStats } from '../../bindings/dji-modem-research/desktop/services';

function NetworkPage() {
  const [connected, setConnected] = useState(false);
  const [connecting, setConnecting] = useState(false);
  const [conn, setConn] = useState<ConnectionInfo | null>(null);
  const [apn, setApn] = useState('3gnet');
  const [socksAddr, setSocksAddr] = useState('127.0.0.1:1080');
  const [socksRunning, setSocksRunning] = useState(false);
  const [stats, setStats] = useState<RelayStats | null>(null);
  const [tunRunning, setTunRunning] = useState(false);
  const [tunStarting, setTunStarting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    try {
      const [isConn, isSocks] = await Promise.all([
        DialerService.IsConnected(),
        DialerService.IsSOCKS5Running(),
      ]);
      setConnected(isConn);
      setSocksRunning(isSocks);
      if (isConn) {
        setConn(await DialerService.GetConnection());
      } else {
        setConn(null);
      }
      if (isSocks) {
        setStats(await DialerService.GetStats());
      } else {
        setStats(null);
      }
    } catch {
      // 静默(services 未就绪时)
    }
  }, []);

  const dial = useCallback(async () => {
    setConnecting(true);
    setError(null);
    try {
      await DialerService.Dial(apn);
      setConnected(true);
      setConn(await DialerService.GetConnection());
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setConnecting(false);
    }
  }, [apn]);

  const hangup = useCallback(async () => {
    setError(null);
    try {
      await DialerService.Hangup();
      setConnected(false);
      setConn(null);
      setSocksRunning(false);
      setStats(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, []);

  const startSocks = useCallback(async () => {
    setError(null);
    try {
      await DialerService.StartSOCKS5(socksAddr);
      setSocksRunning(true);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, [socksAddr]);

  const stopSocks = useCallback(async () => {
    setError(null);
    try {
      await DialerService.StopSOCKS5();
      setSocksRunning(false);
      setStats(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, []);

  const startTUN = useCallback(async () => {
    setTunStarting(true);
    setError(null);
    try {
      await DialerService.StartTUN(apn);
      setTunRunning(true);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setTunStarting(false);
    }
  }, [apn]);

  const stopTUN = useCallback(async () => {
    setError(null);
    try {
      await DialerService.StopTUN();
      setTunRunning(false);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, []);

  useEffect(() => {
    refresh();
    const t = setInterval(refresh, 3000);
    return () => clearInterval(t);
  }, [refresh]);

  return (
    <Flex direction="column" gap="4" p="6">
      <Heading size="6">上网</Heading>

      {error && (
        <Callout.Root color="red" size="1">
          <Callout.Text>{error}</Callout.Text>
        </Callout.Root>
      )}

      <Card size="3">
        <Flex direction="column" gap="3">
          <Flex justify="between" align="center">
            <Heading size="4">QMI 拨号</Heading>
            <Badge color={connected ? 'green' : 'gray'} variant="soft">
              {connected ? '已拨号' : '未拨号'}
            </Badge>
          </Flex>
          {!connected && (
            <TextField.Root placeholder="APN(默认 3gnet)" value={apn} onChange={(e) => setApn(e.target.value)} />
          )}
          {connected ? (
            <Button color="red" variant="soft" onClick={hangup}>断开</Button>
          ) : (
            <Button onClick={dial} disabled={connecting}>
              {connecting ? '拨号中…(约 30s)' : '拨号'}
            </Button>
          )}
        </Flex>
      </Card>

      {connected && conn && (
        <Card size="3">
          <Flex direction="column" gap="2">
            <Heading size="4">连接信息</Heading>
            <Row label="IPv4" value={conn.ipv4Address} />
            <Row label="网关" value={conn.gateway} />
            <Row label="DNS" value={`${conn.dns1}${conn.dns2 ? ', ' + conn.dns2 : ''}`} />
            <Row label="MTU" value={`${conn.mtu}`} />
            {conn.ipv6Address && <Row label="IPv6" value={conn.ipv6Address} />}
          </Flex>
        </Card>
      )}

      {connected && (
        <Card size="3">
          <Flex direction="column" gap="3">
            <Flex justify="between" align="center">
              <Heading size="4">SOCKS5 代理</Heading>
              <Badge color={socksRunning ? 'green' : 'gray'} variant="soft">
                {socksRunning ? '运行中' : '未启动'}
              </Badge>
            </Flex>
            {socksRunning ? (
              <Flex direction="column" gap="1">
                <Text size="2" color="gray">监听地址:<Text as="span" color="iris" highContrast>{socksAddr}</Text></Text>
                <Text size="1" color="gray">curl --socks5-hostname {socksAddr} http://www.baidu.com</Text>
              </Flex>
            ) : (
              <TextField.Root placeholder="监听地址" value={socksAddr} onChange={(e) => setSocksAddr(e.target.value)} />
            )}
            {socksRunning ? (
              <Button color="red" variant="soft" onClick={stopSocks}>停止</Button>
            ) : (
              <Button onClick={startSocks}>启动 SOCKS5(无需 admin)</Button>
            )}
            {socksRunning && stats && (
              <Flex gap="4">
                <Text size="2" color="gray">↑ {stats.txPackets} 包 / {stats.txBytes} B</Text>
                <Text size="2" color="gray">↓ {stats.rxPackets} 包 / {stats.rxBytes} B</Text>
              </Flex>
            )}
          </Flex>
        </Card>
      )}

      <Card size="3">
        <Flex direction="column" gap="3">
          <Flex justify="between" align="center">
            <Heading size="4">TUN 模式(系统级)</Heading>
            <Badge color={tunRunning ? 'green' : 'gray'} variant="soft">
              {tunRunning ? '运行中' : '未启动'}
            </Badge>
          </Flex>
          <Text size="2" color="gray">
            创建系统虚拟网卡,所有流量自动走 4G(无需单独配代理)。<Text color="amber">需管理员密码</Text>。
          </Text>
          <Text size="1" color="gray">断 WiFi 时也能上网(TUN 独立于主机网络)</Text>
          {tunRunning ? (
            <Button color="red" variant="soft" onClick={stopTUN}>停止 TUN</Button>
          ) : (
            <Button onClick={startTUN} disabled={tunStarting}>
              {tunStarting ? '启动中…(输入密码 + 拨号约 30s)' : '启动 TUN(弹密码框)'}
            </Button>
          )}
        </Flex>
      </Card>
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

export default NetworkPage;
