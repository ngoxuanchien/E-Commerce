import { useState } from 'react';
import { useNavigate, useParams } from 'react-router';
import { toast } from 'sonner';
import { useCategories, useProduct } from '@/api/hooks/catalog';
import { useCreateSellerProduct, useUpdateSellerProduct } from '@/api/hooks/seller';
import { ApiError, ERROR_MESSAGES, applyFieldErrors, errorMessage, isRetryable } from '@/api/errors';
import type { Category, Product, ProductInput } from '@/api/types';
import { ErrorState, PageSpinner } from '@/components/states';
import { type ProductFormApi, ProductForm } from './ProductForm';
import { diffProductInput, emptyProduct, fromProduct, productSchema, toProductInput, withExpectedStock } from './product-schema';

export function ProductFormPage() {
  const { id } = useParams();
  const isNew = !id;
  const categories = useCategories();
  const product = useProduct(id ?? '', { enabled: !isNew });

  if (categories.isPending || (!isNew && product.isPending)) return <PageSpinner />;
  if (categories.isError) return <ErrorState error={categories.error} onRetry={() => void categories.refetch()} />;
  if (!isNew && product.isError) return <ErrorState error={product.error} onRetry={() => void product.refetch()} />;

  return (
    <ProductEditor
      key={id ?? 'new'}
      id={id}
      loaded={product.data}
      categories={categories.data}
      reload={async () => (await product.refetch()).data}
    />
  );
}

function ProductEditor({ id, loaded, categories, reload }: {
  id?: string;
  loaded?: Product;
  categories: Category[];
  reload: () => Promise<Product | undefined>;
}) {
  const isNew = !id;
  const create = useCreateSellerProduct();
  const update = useUpdateSellerProduct();
  const navigate = useNavigate();
  // The product as this form loaded it. It is the base of the PATCH diff and the source of expected_stock.
  // A background refetch does not replace it, so the seller's edits survive. A stale save gets 409 and
  // reloads it explicitly.
  const [snapshot, setSnapshot] = useState(loaded);
  const [generation, setGeneration] = useState(0);

  const onSubmit = async (input: ProductInput, form: ProductFormApi) => {
    try {
      if (id && snapshot) {
        const patch = diffProductInput(toProductInput(productSchema.parse(fromProduct(snapshot))), input);
        if (Object.keys(patch).length > 0) await update.mutateAsync({ id, input: withExpectedStock(patch, snapshot.skus) });
      } else {
        await create.mutateAsync(input);
      }
      toast.success(isNew ? 'Đã tạo sản phẩm' : 'Đã lưu sản phẩm');
      navigate('/seller/products');
    } catch (err) {
      if (err instanceof ApiError && err.code === 'CONFLICT' && !isNew) {
        // Stock moved since the form loaded (orders reserved or released it). Reload the form with it.
        toast.error('Tồn kho vừa thay đổi do có đơn hàng mới. Biểu mẫu đã tải lại số liệu mới nhất, vui lòng kiểm tra rồi lưu lại.');
        const fresh = await reload();
        if (fresh) {
          setSnapshot(fresh);
          setGeneration((g) => g + 1);
        }
        return;
      }
      if (applyFieldErrors(err, form.setError, { rename: { image_keys: 'images' } })) {
        form.setError('root', { message: ERROR_MESSAGES.VALIDATION_ERROR });
        return;
      }
      if (!isRetryable(err)) form.setError('root', { message: errorMessage(err) });
    }
  };

  return (
    <div className="space-y-4">
      <h1 className="text-2xl font-bold">{isNew ? 'Thêm sản phẩm' : 'Sửa sản phẩm'}</h1>
      <ProductForm
        key={generation}
        initial={snapshot ? fromProduct(snapshot) : emptyProduct()}
        categories={categories}
        submitLabel={isNew ? 'Tạo sản phẩm' : 'Lưu thay đổi'}
        onSubmit={onSubmit}
      />
    </div>
  );
}
