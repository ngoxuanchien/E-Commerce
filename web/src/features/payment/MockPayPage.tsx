import { useState } from 'react';
import { useNavigate, useParams } from 'react-router';
import { apiBaseUrl } from '@/api/base-url';
import { Button } from '@/components/ui/button';

export function MockPayPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [loading, setLoading] = useState(false);

  const handleOutcome = async (outcome: 'succeed' | 'fail') => {
    if (!id) return;

    try {
      setLoading(true);
      const response = await fetch(`${apiBaseUrl()}/api/__mock/payments/${id}/${outcome}`, {
        method: 'POST',
      });

      if (response.ok) {
        navigate(`/checkout/result?payment_id=${id}`);
      }
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="rounded-lg border bg-card p-8 text-center">
      <h1 className="text-2xl font-bold">Cổng thanh toán giả lập</h1>
      <p className="mt-2 text-sm text-muted-foreground">Trang này chỉ có ở chế độ mock.</p>

      {id && <p className="mt-4 font-mono text-sm text-muted-foreground">{id}</p>}

      <div className="mt-6 flex justify-center gap-3">
        <Button onClick={() => handleOutcome('succeed')} disabled={loading}>
          Thành công
        </Button>
        <Button onClick={() => handleOutcome('fail')} variant="outline" disabled={loading}>
          Thất bại
        </Button>
      </div>
    </div>
  );
}
