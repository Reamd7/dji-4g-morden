import { useState, useEffect, useCallback } from 'react';
import { Card, Button, Text, Flex, TextField, TextArea, Heading, Callout } from '@radix-ui/themes';
import { Events } from '@wailsio/runtime';
import { SMSService } from '../../bindings/dji-modem-research/desktop/services';
import type { SMS } from '../../bindings/dji-modem-research/desktop/services/models';

/** sms:received 事件负载的结构(后端 Emit 的 map)。类型守卫避免 any。 */
interface SMSReceived {
  sender: string;
  content: string;
  timestamp: string;
}

function isSMSReceived(d: unknown): d is SMSReceived {
  if (!d || typeof d !== 'object') return false;
  const o = d as Record<string, unknown>;
  return typeof o.sender === 'string' && typeof o.content === 'string' && typeof o.timestamp === 'string';
}

function SMSPage() {
  const [messages, setMessages] = useState<SMS[]>([]);
  const [loading, setLoading] = useState(false);
  const [recipient, setRecipient] = useState('');
  const [body, setBody] = useState('');
  const [sending, setSending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const list = await SMSService.ListSMS();
      setMessages(list ?? []);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  const send = useCallback(async () => {
    if (!recipient.trim() || !body.trim()) return;
    setSending(true);
    setError(null);
    try {
      await SMSService.SendSMS(recipient.trim(), body.trim());
      setBody('');
      refresh();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setSending(false);
    }
  }, [recipient, body, refresh]);

  const del = useCallback(async (index: number) => {
    setError(null);
    try {
      await SMSService.DeleteSMS(index);
      setMessages((prev) => prev.filter((m) => m.index !== index));
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, []);

  useEffect(() => {
    refresh();
    // 实时收信:后端 Emit "sms:received",event.data 经类型守卫解析(无 any)。
    const off = Events.On('sms:received', (event: { data?: unknown }) => {
      if (isSMSReceived(event.data)) {
        const d = event.data;
        setMessages((prev) => [
          ...prev,
          { index: -1, sender: d.sender, body: d.content, timestamp: d.timestamp },
        ]);
      }
    });
    return () => {
      if (typeof off === 'function') off();
    };
  }, [refresh]);

  return (
    <Flex direction="column" gap="4" p="6">
      <Flex justify="between" align="center">
        <Heading size="6">短信</Heading>
        <Button onClick={refresh} variant="soft" disabled={loading}>
          {loading ? '加载中…' : '刷新'}
        </Button>
      </Flex>

      {error && (
        <Callout.Root color="red" size="1">
          <Callout.Text>{error}</Callout.Text>
        </Callout.Root>
      )}

      <Card size="2">
        <Flex direction="column" gap="2">
          <TextField.Root
            placeholder="收件人号码(如 +8613...)"
            value={recipient}
            onChange={(e) => setRecipient(e.target.value)}
          />
          <TextArea
            placeholder="短信内容"
            value={body}
            onChange={(e) => setBody(e.target.value)}
          />
          <Flex justify="end">
            <Button onClick={send} disabled={sending || !recipient.trim() || !body.trim()}>
              {sending ? '发送中…' : '发送'}
            </Button>
          </Flex>
        </Flex>
      </Card>

      {!loading && messages.length === 0 && (
        <Text color="gray" size="2">SIM 暂无短信(启用设备后可收发)</Text>
      )}

      {messages.map((m, i) => (
        <Card key={`${m.index}-${i}`} size="2">
          <Flex direction="column" gap="1">
            <Flex justify="between" align="center">
              <Text size="2" weight="bold">{m.sender}</Text>
              <Flex gap="2" align="center">
                <Text size="1" color="gray">{m.timestamp}</Text>
                {m.index >= 0 && (
                  <Button size="1" color="red" variant="ghost" onClick={() => del(m.index)}>
                    删除
                  </Button>
                )}
              </Flex>
            </Flex>
            <Text size="2">{m.body}</Text>
          </Flex>
        </Card>
      ))}
    </Flex>
  );
}

export default SMSPage;
