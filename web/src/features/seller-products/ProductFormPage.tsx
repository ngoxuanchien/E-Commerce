import { useNavigate, useParams } from 'react-router';
import { toast } from 'sonner';
import { useCategories, useProduct } from '@/api/hooks/catalog';
import { useCreateSellerProduct, useUpdateSellerProduct } from '@/api/hooks/seller';
import { ERROR_MESSAGES, applyFieldErrors, errorMessage, isRetryable } from '@/api/errors';
import type { ProductInput } from '@/api/types';
import { ErrorState, PageSpinner } from '@/components/states';
import { type ProductFormApi, ProductForm } from './ProductForm';
import { emptyProduct, fromProduct } from './product-schema';

export function ProductFormPage() {
  const { id } = useParams();
  const isNew = !id;
  const categories = useCategories();
  const product = useProduct(id ?? '', { enabled: !isNew });
  const create = useCreateSellerProduct();
  const update = useUpdateSellerProduct();
  const navigate = useNavigate();

  if (categories.isPending || (!isNew && product.isPending)) return <PageSpinner />;
  if (categories.isError) return <ErrorState error={categories.error} onRetry={() => void categories.refetch()} />;
  if (!isNew && product.isError) return <ErrorState error={product.error} onRetry={() => void product.refetch()} />;

  const onSubmit = async (input: ProductInput, form: ProductFormApi) => {
    try {
      if (id) await update.mutateAsync({ id, input });
      else await create.mutateAsync(input);
      toast.success(isNew ? 'Đã tạo sản phẩm' : 'Đã lưu sản phẩm');
      navigate('/seller/products');
    } catch (err) {
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
        key={product.data?.updated_at ?? 'new'}
        initial={product.data ? fromProduct(product.data) : emptyProduct()}
        categories={categories.data}
        submitLabel={isNew ? 'Tạo sản phẩm' : 'Lưu thay đổi'}
        onSubmit={onSubmit}
      />
    </div>
  );
}
